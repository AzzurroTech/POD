package main

import (
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// -------------------------------------------------------------------
// Global state (maps + mutex)
// -------------------------------------------------------------------
var (
	mu            sync.RWMutex               // protects everything below
	keyToFiles    = make(map[string][]string) // key   → []filenames containing that key
	valueToFiles  = make(map[string][]string) // value → []filenames containing that value
	storedFiles   []string                    // ordered list of all saved filenames (without extension)
	storageFolder = "./forms"                 // directory where HTML files are written
)

// -------------------------------------------------------------------
// Templates
// -------------------------------------------------------------------
var listTmpl = template.Must(template.New("list").Parse(`
<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><title>Matching Forms</title></head>
<body>
<h1>Forms matching the supplied query parameters</h1>
{{if .Files}}
<ul>
	{{range .Files}}
	<li><a href="/forms/{{.}}">{{.}}</a></li>
	{{end}}
</ul>
{{else}}
<p>No stored form matches the current query.</p>
{{end}}
<hr>
<h2>Submit a new form</h2>
<form method="post" action="/">
	<label>Key: <input name="key" required></label><br>
	<label>Value: <input name="value" required></label><br>
	<button type="submit">Save form</button>
</form>
</body>
</html>
`))

// -------------------------------------------------------------------
// Helper: write a simple HTML file that recreates the submitted form
// -------------------------------------------------------------------
func writeFormFile(values map[string][]string) (string, error) {
	ts := time.Now().UnixNano()
	baseName := fmt.Sprintf("form_%d", ts)          // without .html
	filename := baseName + ".html"                  // on‑disk name
	path := filepath.Join(storageFolder, filename) // full path

	// Ensure the storage directory exists.
	if err := os.MkdirAll(storageFolder, 0755); err != nil {
		return "", err
	}

	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	// Minimal HTML page that contains a form with hidden inputs for each field.
	fmt.Fprintf(f, `<!DOCTYPE html>
<html lang="en">
<head><meta charset="UTF-8"><title>Saved Form %s</title></head>
<body>
<h1>Saved Form</h1>
<form method="post" action="/">
`, filename)

	for k, vals := range values {
		for _, v := range vals {
			fmt.Fprintf(f,
				`<input type="hidden" name="%s" value="%s">`+"\n",
				template.HTMLEscapeString(k),
				template.HTMLEscapeString(v))
		}
	}
	fmt.Fprint(f, `<button type="submit">Resubmit</button>
</form>
</body>
</html>`)

	return baseName, nil // return the name **without** extension for the maps
}

// -------------------------------------------------------------------
// Helper: update the lookup maps for a newly stored file
// -------------------------------------------------------------------
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

// -------------------------------------------------------------------
// Helper: compute intersection of file sets for the supplied query.
// Returns the slice of base filenames (no .html) that contain **all**
// queried keys and at least one of the requested values per key.
// -------------------------------------------------------------------
func filesMatchingQuery(query map[string][]string) []string {
	mu.RLock()
	defer mu.RUnlock()

	if len(query) == 0 {
		cpy := make([]string, len(storedFiles))
		copy(cpy, storedFiles)
		return cpy
	}

	var candidateSet map[string]struct{}
	first := true

	for qk, qvs := range query {
		filesWithKey := keyToFiles[qk]

		tmp := make(map[string]struct{})
		for _, fn := range filesWithKey {
			tmp[fn] = struct{}{}
		}

		if len(qvs) > 0 {
			filtered := make(map[string]struct{})
			for _, val := range qvs {
				for _, fn := range valueToFiles[val] {
					if _, ok := tmp[fn]; ok {
						filtered[fn] = struct{}{}
					}
				}
			}
			tmp = filtered
		}

		if first {
			candidateSet = tmp
			first = false
		} else {
			newSet := make(map[string]struct{})
			for fn := range candidateSet {
				if _, ok := tmp[fn]; ok {
					newSet[fn] = struct{}{}
				}
			}
			candidateSet = newSet
		}
		if len(candidateSet) == 0 {
			break
		}
	}

	var result []string
	for _, fn := range storedFiles {
		if _, ok := candidateSet[fn]; ok {
			result = append(result, fn)
		}
	}
	return result
}

// -------------------------------------------------------------------
// Path sanitisation – reject any attempt to escape the storage folder.
// -------------------------------------------------------------------
func isPathSafe(requested string) bool {
	clean := filepath.Clean("/" + requested) // make absolute for cleaning
	expectedPrefix := "/" + strings.TrimPrefix(storageFolder, "./")
	return strings.HasPrefix(clean, expectedPrefix)
}

// -------------------------------------------------------------------
// HTTP handler
// -------------------------------------------------------------------
func handler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// -----------------------------------------------------------
		// 1️⃣  Serve a stored form when the URL points to /forms/<id>
		// -----------------------------------------------------------
		if strings.HasPrefix(r.URL.Path, "/forms/") && r.URL.Path != "/forms/" {
			trimmed := strings.TrimPrefix(r.URL.Path, "/forms/") // e.g. "form_17012345"
			if isPathSafe(trimmed) {
				// Append .html to locate the real file on disk.
				fileOnDisk := filepath.Join(storageFolder, trimmed+".html")
				if info, err := os.Stat(fileOnDisk); err == nil && !info.IsDir() {
					http.ServeFile(w, r, fileOnDisk)
					return
				}
			}
			http.NotFound(w, r)
			return
		}

		// -----------------------------------------------------------
		// 2️⃣  Otherwise treat it as the “match‑by‑key/value” endpoint.
		// -----------------------------------------------------------
		query := r.URL.Query()
		matching := filesMatchingQuery(query)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := listTmpl.Execute(w, struct{ Files []string }{matching}); err != nil {
			http.Error(w, "template error", http.StatusInternalServerError)
		}
	case http.MethodPost:
		// -----------------------------------------------------------
		// Store a new form and update the maps.
		// -----------------------------------------------------------
		if err := r.ParseForm(); err != nil {
			http.Error(w, "cannot parse form", http.StatusBadRequest)
			return
		}
		if len(r.PostForm) == 0 {
			http.Error(w, "empty form", http.StatusBadRequest)
			return
		}

		baseName, err := writeFormFile(r.PostForm) // baseName = "form_12345"
		if err != nil {
			http.Error(w, "failed to write file", http.StatusInternalServerError)
			return
		}
		indexFile(baseName, r.PostForm)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w,
			`<html><body>Form saved as <a href="/forms/`+baseName+`">`+baseName+
				`</a>. <a href="/">Back</a></body></html>`)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// -------------------------------------------------------------------
// Main entry point
// -------------------------------------------------------------------
func main() {
	// Ensure the storage directory exists before the server starts.
	if err := os.MkdirAll(storageFolder, 0755); err != nil {
		panic(fmt.Sprintf("cannot create storage folder: %v", err))
	}

	http.HandleFunc("/", handler)

	port := "8080"
	fmt.Printf("Server listening on http://localhost:%s/\n", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		panic(err)
	}
}