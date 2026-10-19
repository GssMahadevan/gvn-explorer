// Scenario definitions for the demo videos (consumed by record.js).
//   standalone: initial setup only (the end card points to the full tour).
//   docker:     the complete feature tour: users (password + API key),
//               groups, tenants, files create/edit/save, markdown
//               view+edit, YAML tree editor with draft+publish, per-path
//               permission grants, built-in help.
// Narration ("say") is written to roughly cover each scene's action time;
// record.js still floors the scene length at the estimated action time.

const setupTerm = { type: "term", caption: "Download the archive → unpack → run. That's the whole install.",
  lines: [
    { cmd: "curl -LO https://github.com/GssMahadevan/gvn-explorer/releases/download/v0.1.0/gvn-explorer-v0.1.0-linux-amd64.tar.gz" },
    { cmd: "tar xzf gvn-explorer-v0.1.0-linux-amd64.tar.gz" },
    { cmd: "cd gvn-explorer-v0.1.0-linux-amd64" },
    { cmd: "./gvn-explorer-v0.1.0-linux-amd64" },
    { out: "INFO  file root        path=/srv/files" },
    { out: "INFO  web gui enabled  dir=(embedded)" },
    { out: "INFO  server starting  addr=:8080" },
  ],
  say: "Grab the archive for your platform from the releases page. Unpack it, and run the binary. That is the whole install. The web console and the full user guide live inside the binary itself, and on first run it creates its data folder and database." };

const loginSteps = (user = "admin", pass = "admin") => [
  { waitSel: 'input[autocomplete="organization"]' },
  { type: { sel: 'input[autocomplete="organization"]', text: "system" } },
  { type: { sel: 'input[autocomplete="username"]', text: user } },
  { type: { sel: 'input[autocomplete="current-password"]', text: pass } },
  { clickText: { sel: "button", text: "login" } },
  { waitSel: ".app-user" },
];

const SCENARIOS = {
  standalone: {
    out: "docs/demo-standalone.mp4",
    scenes: [
      { type: "card", title: "gvn-explorer", sub: "Standalone — one self-contained binary",
        caption: "A self-hosted web file explorer — no install, no Docker",
        say: "Try g-v-n explorer standalone: one self-contained binary. No install, and no Docker." },
      setupTerm,
      { type: "gui", url: "/", caption: "Open http://localhost:8080 — first-run admin: system / admin / admin",
        say: "Now open localhost colon 8080 in a browser. Sign in with the first-run admin: tenant system, user admin, password admin. Change that password right away in a real deployment.",
        steps: loginSteps() },
      { type: "card", title: "data/ + perms.db", sub: "…that's the whole setup. Upgrade = swap the binary.",
        caption: "Full feature tour: docs/demo-docker.mp4 — same app, same image",
        say: "Back up one folder and one file — that is the whole setup. For the complete feature tour, watch the Docker clip: same app, the very image we ship." },
    ],
  },

  docker: {
    out: "docs/demo-docker.mp4",
    scenes: [
      { type: "card", title: "gvn-explorer", sub: "Docker — the full tour",
        caption: "Every major feature, running from one container",
        say: "The complete tour of g-v-n explorer: every major feature, running from a single Docker container." },
      { type: "term", caption: "Your files at /data/root · accounts+audit at /data (back this up!)",
        lines: [
          { cmd: 'mkdir -p files state' },
          { cmd: 'docker run -d --name gvn -p 8080:8080 \\\n  -v "$PWD/files:/data/root" \\\n  -v "$PWD/state:/data" \\\n  ghcr.io/gssmahadevan/gvn-explorer:latest' },
          { cmd: "docker logs -f gvn" },
          { out: 'INFO  file root        path=/data/root' },
          { out: 'INFO  web gui enabled  dir=/app/web' },
          { out: 'INFO  server starting  addr=:8080' },
        ],
        say: "Run the published multi-arch image with two volumes: your file tree at slash data slash root, and slash data for accounts, permissions, and the audit trail — that is the one to back up. The container starts with a health-check and structured JSON logs." },
      { type: "gui", url: "/", caption: "localhost:8080 — the super admin comes from the ADMIN env (default system/admin/admin)",
        say: "Open localhost colon 8080. Sign in as the super admin from the A D M I N variable — by default system, admin, admin.",
        steps: loginSteps() },
      { type: "gui", url: "/", caption: "Users: create · set password · API key (shown once) · suspend",
        say: "Accounts first. Users belong to a tenant. Create user: give it a password — or leave the password blank and it becomes a device that authenticates with API keys only. Every row menu: edit, set a new password, hand out an API key, shown exactly once, suspend, or delete.",
        steps: [
          { tab: "users" },
          { btn: "Create user" }, { wait: 800 },
          { fill: { sel: "#username", text: "demo" } },
          { fill: { sel: "#password", text: "demo12345" } },
          { mbtn: "Create user" }, { wait: 1300 },
          { rowMenu: { row: "demo", item: "set password" } }, { wait: 800 },
          { fill: { sel: "#password", text: "demo98765" } },
          { mbtn: "Set password" }, { wait: 1000 },
          { rowMenu: { row: "demo", item: "new API key" } }, { wait: 1600 },
          { mbtn: "Done" }, { wait: 600 },
        ] },
      { type: "gui", url: "/", caption: "Groups: grant once, every member inherits",
        say: "Groups let you grant once and cover everyone in them.",
        steps: [
          { tab: "groups" },
          { btn: "Create group" }, { wait: 800 },
          { fill: { sel: "#name", text: "devs" } },
          { mbtn: "Create group" }, { wait: 1000 },
        ] },
      { type: "gui", url: "/", caption: "Tenants: customers fully isolated from each other",
        say: "Tenants keep customers apart: each has its own users, groups and grants. A tenant admin manages only its own — only you, the bootstrap super admin, see across tenants.",
        steps: [
          { tab: "tenants" },
          { btn: "Create tenant" }, { wait: 800 },
          { fill: { sel: "#name", text: "acme" } },
          { fill: { sel: "#description", text: "customer acme" } },
          { mbtn: "Create tenant" }, { wait: 1200 },
        ] },
      { type: "gui", url: "/", caption: "Files: new folder, new file — plain files on disk",
        say: "Your files stay plain files on disk. Create a folder, create a file. In the editor, typing and Save write text files just like you would expect.",
        steps: [
          { btn: "Folder" }, { wait: 700 },
          { fill: { sel: '[placeholder="folder name"]', text: "team" } },
          { mbtn: "Create" }, { wait: 1300 },
          { btn: "File" }, { wait: 700 },
          { fill: { sel: '[placeholder="file name"]', text: "team.md" } },
          { mbtn: "Create" }, { wait: 1500 },
          { clickSel: '[data-testid="pane-edit"]' }, { wait: 900 },
          { rowMenu: { row: "team.md", item: "edit" } }, { wait: 1200 },
          { cm: "# team standup\n\n- deploy via docker\n" },
          { clickSel: '[data-testid="editor-save"]' }, { wait: 1500 },
          { clickSel: '[data-testid="pane-back"]' }, { wait: 900 },
        ] },
      { type: "gui", url: "/", caption: "Markdown opens rendered · edit from the viewer",
        say: "Markdown opens rendered — and editing happens right from the viewer, with a formatting ribbon. Save, and it is on disk.",
        steps: [
          { clickText: { sel: "tbody button", text: "team.md" } }, { wait: 1600 },
          { clickSel: '[data-testid="pane-edit"]' }, { wait: 1000 },
          { cm: "\n- shipped the demo videos\n" },
          { clickSel: '[data-testid="editor-save"]' }, { wait: 1200 },
          { clickSel: '[data-testid="pane-back"]' }, { wait: 900 },
        ] },
      { type: "gui", url: "/", caption: "YAML: tree editor · comments survive · draft → Publish to file",
        say: "And Yaml config gets a structured editor: drill down the tree, edit a value — the edit autosaves as an unpublished draft. Publish to file commits it, and comments and layout survive untouched.",
        steps: [
          { clickText: { sel: "tbody button", text: "projects" } }, { wait: 900 },
          { clickText: { sel: "tbody button", text: "deploy.yml" } }, { wait: 1600 },
          { clickSel: '[data-testid="pane-edit"]' }, { wait: 1200 },
          { yamlOpen: "metadata" }, { wait: 800 },
          { setLeaf: { match: "explorer", text: "explorer-eu" } }, { wait: 2800 },
          { clickSel: '[data-testid="editor-publish"]' }, { wait: 1600 },
          { clickSel: '[data-testid="pane-back"]' }, { wait: 900 },
        ] },
      { type: "gui", url: "/", caption: "Permissions: per-path grants · inherit down · optional expiry",
        say: "Access control is per path: a grant applies to that folder and inherits down, the nearest grant wins, and no grant at all hides the subtree. Any grant can carry an expiry — that is how time-boxed customer shares work.",
        steps: [
          { search: { sel: 'input[placeholder="search name"]', text: "projects" } },
          { rowMenu: { row: "projects", item: "permissions" } }, { wait: 1600 },
          { btn: "^Grant access$" }, { wait: 1200 },
          { typeIn: { sel: '[data-testid="grant-principal"] input', text: "demo" } }, { wait: 1600 },
          { opt: "demo" },
          { mbtn: "Save grant" }, { wait: 1800 },
        ] },
      { type: "gui", url: "/", caption: "The complete manual ships inside the app — Help",
        say: "Everything else — sharing, the audit trail, the REST API with copy-paste curl — is documented inside the app: Help, in the menu bar.",
        steps: [
          { clickSel: ".app-brand" }, { wait: 700 },
          { clickText: { sel: ".ant-dropdown [role='menuitem'], .ant-menu-item", text: "Help" } }, { wait: 2200 },
        ] },
      { type: "card", title: "ghcr.io/gssmahadevan/gvn-explorer", sub: "or the single binary — same app, same version",
        caption: "Upgrade = re-pull / swap binary · state lives in your volume",
        say: "All of that runs from the image on ghcr, or from the single binary in the standalone clip. Your state lives in your own volume, and an upgrade is just a re-pull." },
    ],
  },
};

module.exports = { SCENARIOS };
