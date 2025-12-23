POD – Personal Online Dashboard
by Azzurro Technology Inc.

Table of Contents

Overview
Features
Architecture
Prerequisites
Installation
Running the Server
Configuration
Usage Guide
API Reference
Testing
Contributing
Roadmap
License
Contact & Support


Overview
POD (Personal Online Dashboard) is a lightweight, self‑contained web application that lets users:

Log in or bypass authentication for a guest session.
Create, edit, and store custom HTML forms via an intuitive modal UI (Dynamic Forms).
Persist UI state securely – the dashboard’s context is encrypted client‑side with AES‑GCM and stored only as ciphertext on the server.
Import external HTML forms and reuse them as templates throughout the dashboard.
Process query parameters only after a successful login (or explicit guest bypass).

All HTML, CSS, and JavaScript are generated at runtime using Go’s html/template package – there are no external static files required.

Features
✅Feature🔐Secure password storage (salt + SHA‑256)🗝️Per‑user encrypted UI context (AES‑GCM)👤Guest‑bypass mode (?bypass=1)📄Dynamic Form Builder (modal)📥Import arbitrary HTML forms as reusable <template> elements📊In‑memory key/value maps for fast look‑ups📁Tiny HTML files generated for each query payload🌐Pure Go standard library – no third‑party dependencies🧪Comprehensive unit tests (see *_test.go)📦Single‑binary deployment (Docker optional)

Architecture
+-------------------+        +---------------------------+
|   HTTP Handlers   | <----> |   In‑Memory Stores        |
| (login, register, |        | - users (salt, hash, enc) |
|  app, API, etc.)  |        | - sessions (sid → user)   |
+-------------------+        | - templates (filename →   |
          ^                 |   raw <template> markup) |
          |                 | - key/value maps (key →   |
          |                 |   filenames, value → ...) |
          |                 +---------------------------+
          |
          v
+-------------------+        +---------------------------+
|  HTML Templates   | <----> |  Client‑Side JavaScript   |
| (login.html,      |        | - Web Crypto API (AES‑GCM)|
|  register.html,   |        | - UI state serialization |
|  app.html)        |        | - Dynamic Forms modal     |
+-------------------+        +---------------------------+

All communication between client and server is JSON over HTTPS (HTTPS is recommended in production).

Prerequisites
ToolMinimum VersionGo1.22GitanyDocker (optional)20.10+

Installation
# Clone the repository
git clone https://github.com/azzurrotech/pod.git
cd pod

# Build the binary (pure stdlib)
go build -o pod .
Alternatively, use Docker:
# Dockerfile
FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY . .
RUN go build -o /pod .

FROM alpine:3.20
COPY --from=builder /pod /usr/local/bin/pod
EXPOSE 8080
ENTRYPOINT ["pod"]
docker build -t azzuro/pod .
docker run -p 8080:8080 azzuro/pod

Running the Server
# Default (listens on :8080)
./pod
The server will automatically:

Load HTML templates from templates/ (login, register, app).
Start listening on http://localhost:8080/app.

Visit the URL in a browser to begin.
Environment Variables (optional)
VariableDescriptionDefaultPORTPort number for the HTTP server8080DATA_DIRDirectory where tiny form files are stored (./forms by default)./formsTEMPLATE_DIRDirectory for persisted <template> files (used only if you enable persistence)./templates_store
You can set them before launching:
export PORT=9090
export DATA_DIR=/var/pod/forms
./pod

Configuration
All configuration lives in config.go (generated at compile‑time).
If you need to tweak defaults, edit the constants at the top of the file and rebuild.

Usage Guide
1️⃣ Register / Log In

Register: http://localhost:8080/register – choose a username & password.
Login: http://localhost:8080/login – after login you’ll be redirected to the dashboard.

2️⃣ Guest Mode
Append ?bypass=1 to the dashboard URL:
http://localhost:8080/app?bypass=1

A temporary session is created; no data is persisted.
3️⃣ Building a Form

Click “🛠️ Open Dynamic Forms”.
Choose type, name, optional attributes, then Add to Form.
The preview updates in real‑time.
Click 💾 Save Form as Template – you’ll be prompted for a template name.
The new template appears in the Add a component panel and can be inserted anywhere in the dashboard.

4️⃣ Importing an External HTML Form

Use the Import a form (HTML) section.
Select a .html or .htm file and click Submit.
The file is wrapped in a <template> and added to the template library.

5️⃣ Saving & Restoring UI State

Export: Click 💾 Download JSON – the current dashboard state (templates, layout, etc.) is saved as combined-ux.json.
Import: Click 📂 Upload JSON, select a previously exported file, and the dashboard restores automatically (you’ll be prompted for your password to decrypt the stored context).

6️⃣ Processing Query Parameters
Authenticated users can hit:
GET /api/query?key1=value1&key2=value2

The server:

Stores the key/value pairs in in‑memory maps.
Writes a tiny HTML file (forms/form_<timestamp>.html) that recreates the data as hidden inputs.
Returns a JSON response with the stored filename.


API Reference
EndpointMethodAuthDescription/loginGET/POST❌Render login page / process credentials/registerGET/POST❌Render registration page / create account/logoutGET✅Destroy session/appGET✅ (or ?bypass=1)Main dashboard UI/api/saveContextPOST✅Receive encrypted UI context ({ "enc": "<base64>" })/api/queryGET✅Store query parameters as a form file/importPOST✅Multipart upload of an HTML form (creates a <template>)/templates/manifest.jsonGET✅List of stored template filenames/templates/<name>.htmlGET✅Raw <template> markup for a given file
All responses are JSON unless otherwise noted.

Testing
The repository includes a suite of unit tests covering:

User registration & authentication
Session handling
Encryption/decryption round‑trips
Query‑parameter processing
Template import/export

Run them with:
go test ./...
For continuous integration, the provided GitHub Actions workflow runs the tests on every PR.

Contributing
We welcome contributions! Please follow these steps:

Fork the repo and create a feature branch.
Write tests for any new functionality.
Ensure go vet and golint (if installed) report no issues.
Submit a Pull Request with a clear description of the change.

Read our full contributing guide in CONTRIBUTING.md (to be added soon).

Roadmap

v1.1 – Add OAuth2 login (Google, GitHub).
v1.2 – Persistent storage (SQLite) for user records & templates.
v2.0 – Multi‑tenant dashboards with role‑based permissions.
v2.1 – Real‑time collaborative editing via WebSockets.

Feel free to open an issue to propose new features!

License
Distributed under the MIT License. See LICENSE for full text.

Contact & Support

Website: https://azzurro.tech
Email: info@azzurro.tech

For bugs or feature requests, open an issue on GitHub.

Happy dashboard building! 🚀