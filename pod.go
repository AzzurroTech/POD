package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

/*
	"crypto/aes"
	"crypto/cipher"
		"net/url"
*/

/* ----------------------------------------------------------------------
   1️⃣  GLOBAL STATE
   ---------------------------------------------------------------------- */
type userRec struct {
	Salt          []byte // 16‑byte random salt (plain)
	PassHash      []byte // SHA‑256(salt‖password)
	EncContextB64 string // base64‑encoded AES‑GCM ciphertext of the UI context
}

// In‑memory user DB and session store
var (
	mu          sync.RWMutex
	users       = make(map[string]*userRec) // username → record
	sessions    = make(map[string]string)   // sessionID → username (empty = guest)
	nextSessNum int64 = 1
)

// Key/value maps used by the original form‑storage logic
var (
	keyToFiles   = make(map[string][]string) // key   → []filenames
	valueToFiles = make(map[string][]string) // value → []filenames
	storedFiles  []string                    // ordered list of filenames (no .html)
	storageDir   = "./forms"                 // where tiny HTML files are written
)

// In‑memory storage for imported HTML templates
var (
	templatesMu sync.RWMutex
	templates   = make(map[string]string) // filename → raw HTML (includes <template> wrapper)
)

/* ----------------------------------------------------------------------
   2️⃣  SESSION & CRYPTO HELPERS
   ---------------------------------------------------------------------- */
func newSession(username string) string {
	mu.Lock()
	defer mu.Unlock()
	id := fmt.Sprintf("%d-%d", time.Now().UnixNano(), nextSessNum)
	nextSessNum++
	sessions[id] = username
	return id
}

func getUsername(r *http.Request) string {
	c, err := r.Cookie("sid")
	if err != nil {
		return ""
	}
	mu.RLock()
	defer mu.RUnlock()
	return sessions[c.Value]
}

func setSIDCookie(w http.ResponseWriter, sid string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "sid",
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(24 * time.Hour),
	})
}

// ---- Password handling -------------------------------------------------
func genSalt() []byte {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}
func hashPassword(salt []byte, password string) []byte {
	h := sha256.New()
	h.Write(salt)
	h.Write([]byte(password))
	return h.Sum(nil)
}

/* ----------------------------------------------------------------------
   3️⃣  TEMPLATE VARIABLES (filled at startup)
   ---------------------------------------------------------------------- */
var (
	loginTmpl    *template.Template
	registerTmpl *template.Template
	appTmpl      *template.Template
)

/* ----------------------------------------------------------------------
   4️⃣  TEMPLATE LOADING (executed once in main)
   ---------------------------------------------------------------------- */
func loadTemplates() error {
	var err error
	loginTmpl, err = template.ParseFiles(filepath.Join("templates", "login.html"))
	if err != nil {
		return fmt.Errorf("loading login.html: %w", err)
	}
	registerTmpl, err = template.ParseFiles(filepath.Join("templates", "register.html"))
	if err != nil {
		return fmt.Errorf("loading register.html: %w", err)
	}
	appTmpl, err = template.ParseFiles(filepath.Join("templates", "app.html"))
	if err != nil {
		return fmt.Errorf("loading app.html: %w", err)
	}
	return nil
}

/* ----------------------------------------------------------------------
   5️⃣  HANDLERS
   ---------------------------------------------------------------------- */

// ----- Root redirect ("/" → "/app") ------------------------------------
func rootRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/app", http.StatusTemporaryRedirect)
}

// ----- Login ---------------------------------------------------------
func loginHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Preserve any original query string so we can forward it after login
		redirect := r.URL.RawQuery
		loginTmpl.Execute(w, map[string]string{
			"Error":    "",
			"Redirect": redirect,
		})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		username := r.FormValue("username")
		password := r.FormValue("password")
		redirect := r.FormValue("redirect")

		mu.RLock()
		rec, ok := users[username]
		mu.RUnlock()
		if !ok || !bytes.Equal(rec.PassHash, hashPassword(rec.Salt, password)) {
			// Invalid credentials – redisplay login with error
			loginTmpl.Execute(w, map[string]string{
				"Error":    "Invalid credentials",
				"Redirect": redirect,
			})
			return
		}
		// Successful login
		sid := newSession(username)
		setSIDCookie(w, sid)

		// Redirect to the original destination (or /app)
		target := "/app"
		if redirect != "" {
			target += "?" + redirect
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ----- Registration ---------------------------------------------------
func registerHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		registerTmpl.Execute(w, map[string]string{"Error": ""})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		username := r.FormValue("username")
		password := r.FormValue("password")

		mu.Lock()
		if _, exists := users[username]; exists {
			mu.Unlock()
			registerTmpl.Execute(w, map[string]string{"Error": "Username already taken"})
			return
		}
		salt := genSalt()
		rec := &userRec{
			Salt:     salt,
			PassHash: hashPassword(salt, password),
		}
		users[username] = rec
		mu.Unlock()

		// Auto‑login after registration
		sid := newSession(username)
		setSIDCookie(w, sid)

		// Render the main UI (no saved context yet)
		appTmpl.Execute(w, map[string]string{
			"Username":   username,
			"SaltB64":    base64.StdEncoding.EncodeToString(salt),
			"EncCtxB64":  "",
			"Bypass":    "0",
		})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ----- Logout --------------------------------------------------------
func logoutHandler(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("sid")
	if err == nil {
		mu.Lock()
		delete(sessions, c.Value)
		mu.Unlock()
		// Expire the cookie
		http.SetCookie(w, &http.Cookie{
			Name:   "sid",
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ----- Main UI --------------------------------------------------------
func appHandler(w http.ResponseWriter, r *http.Request) {
	username := getUsername(r)
	bypass := r.URL.Query().Get("bypass") == "1"

	// Force login if not authenticated and not bypassing
	if username == "" && !bypass {
		loginURL := "/login"
		if raw := r.URL.RawQuery; raw != "" {
			loginURL += "?" + raw
		}
		http.Redirect(w, r, loginURL, http.StatusSeeOther)
		return
	}

	// Guest (bypass) gets a temporary session ID so the UI can set a cookie
	if username == "" && bypass {
		sid := newSession("")
		setSIDCookie(w, sid)
	}

	// Pull user record (if any) to get salt & encrypted context
	var saltB64, encB64 string
	if username != "" {
		mu.RLock()
		if rec, ok := users[username]; ok {
			saltB64 = base64.StdEncoding.EncodeToString(rec.Salt)
			encB64 = rec.EncContextB64
		}
		mu.RUnlock()
	}

	// Render the Combined‑UX page
	appTmpl.Execute(w, map[string]string{
		"Username":   username,
		"SaltB64":    saltB64,
		"EncCtxB64":  encB64,
		"Bypass":    strconv.FormatBool(bypass),
	})
}

// ----- Save encrypted UI context (sent from client) -------------------
func saveContextHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username := getUsername(r)
	if username == "" {
		// Guests cannot persist a context – just ignore
		w.WriteHeader(http.StatusOK)
		return
	}
	var payload struct {
		Enc string `json:"enc"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	mu.Lock()
	if rec, ok := users[username]; ok {
		rec.EncContextB64 = payload.Enc
	}
	mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// ----- Process authenticated query parameters ------------------------
func queryHandler(w http.ResponseWriter, r *http.Request) {
	username := getUsername(r)
	if username == "" {
		http.Error(w, "unauthorized – please log in", http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	if len(q) == 0 {
		http.Error(w, "no query parameters supplied", http.StatusBadRequest)
		return
	}
	// Convert query map to the shape the original program used
	formVals := make(map[string][]string)
	for k, vs := range q {
		formVals[k] = vs
	}
	// Write tiny HTML file and index it
	base, err := writeFormFile(formVals)
	if err != nil {
		http.Error(w, "failed to write file", http.StatusInternalServerError)
		return
	}
	indexFile(base, formVals)

	// Respond with JSON
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status": "stored",
		"file":   base,
	})
}

// ----- Import an HTML form (multipart upload) ------------------------
func importHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MiB limit
		http.Error(w, "cannot parse multipart form", http.StatusBadRequest)
		return
	}
	file, hdr, err := r.FormFile("formfile")
	if err != nil {
		http.Error(w, "missing formfile", http.StatusBadRequest)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "cannot read uploaded file", http.StatusInternalServerError)
		return
	}
	// Wrap the raw HTML in a <template> tag and store it in memory
	wrapped := fmt.Sprintf("<template data-name=\"%s\">\n%s\n</template>", hdr.Filename, string(content))

	templatesMu.Lock()
	templates[hdr.Filename] = wrapped
	templatesMu.Unlock()

	// Respond with JSON so the client can refresh its UI
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// ----- Serve the list of template filenames (manifest) ---------------
func templatesManifestHandler(w http.ResponseWriter, r *http.Request) {
	templatesMu.RLock()
	names := make([]string, 0, len(templates))
	for n := range templates {
		names = append(names, n)
	}
	templatesMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(names)
}

// ----- Serve a single template file (raw <template> markup) ----------
func templateFileHandler(w http.ResponseWriter, r *http.Request) {
	// Expected URL: /templates/<filename>
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		http.NotFound(w, r)
		return
	}
	filename := parts[2]

	templatesMu.RLock()
	data, ok := templates[filename]
	templatesMu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	io.WriteString(w, data)
}

/* ----------------------------------------------------------------------
   6️⃣  UTILITY: WRITE FORM FILE & INDEX IT
   ---------------------------------------------------------------------- */
func writeFormFile(values map[string][]string) (string, error) {
	ts := time.Now().UnixNano()
	base := fmt.Sprintf("form_%d", ts) // no .html extension in the map
	path := filepath.Join(storageDir, base+".html")

	if err := os.MkdirAll(storageDir, 0755); err != nil {
		return "", err
	}
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Minimal HTML that recreates the submitted key/value pairs
	fmt.Fprintf(f, `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><title>%s</title></head><body>
<form>`, base)
	for k, vals := range values {
		for _, v := range vals {
			fmt.Fprintf(f, `<input type="hidden" name="%s" value="%s">`,
				template.HTMLEscapeString(k), template.HTMLEscapeString(v))
		}
	}
	fmt.Fprint(f, `<button type="submit">Resubmit</button></form></body></html>`)

	return base, nil // return the base name (without .html)
}

func indexFile(baseName string, values map[string][]string) {
	mu.Lock()
	defer mu.Unlock()
	storedFiles = append(storedFiles, baseName)

	for k, vals := range values {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		keyToFiles[k] = append(keyToFiles[k], baseName)

		for _, v := range vals {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			valueToFiles[v] = append(valueToFiles[v], baseName)
		}
	}
}

/* ----------------------------------------------------------------------
   7️⃣  SERVER STARTUP
   ---------------------------------------------------------------------- */
func main() {
	// -------------------------------------------------------------
	// Load HTML templates from the ./templates directory
	// -------------------------------------------------------------
	if err := loadTemplates(); err != nil {
		log.Fatalf("failed to load templates: %v", err)
	}

	// -------------------------------------------------------------
	// Route registration
	// -------------------------------------------------------------
	http.HandleFunc("/", rootRedirect)
	http.HandleFunc("/login", loginHandler)
	http.HandleFunc("/register", registerHandler)
	http.HandleFunc("/logout", logoutHandler)
	http.HandleFunc("/app", appHandler)

	// API endpoints
	http.HandleFunc("/api/saveContext", saveContextHandler)
	http.HandleFunc("/api/query", queryHandler)

	// Import HTML form (multipart upload)
	http.HandleFunc("/import", importHandler)

	// Template serving endpoints
	http.HandleFunc("/templates/manifest.json", templatesManifestHandler)
	// Anything under /templates/ (except manifest) serves a single template file
	http.HandleFunc("/templates/", templateFileHandler)

	// -------------------------------------------------------------
	// Start the HTTP server
	// -------------------------------------------------------------
	port := "8080"
	log.Printf("🚀 Server listening on http://localhost:%s/app", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("server error: %v", err)
	}
}