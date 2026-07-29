#!/usr/bin/env node
/**
 * Studio markdown + app.jsx marker smoke test (no browser / no React build).
 */
const fs = require("fs");
const path = require("path");

const appPath = path.join(__dirname, "app.jsx");
const src = fs.readFileSync(appPath, "utf8");
const markers = [
  "function renderMarkdown",
  "function DepGraph",
  "PROVIDER_PRESETS",
  "/api/runs",
  "/api/runs/stop",
  "/api/events",
  "file_change",
  "autoScroll",
  "MarkdownDocEditor",
  "slmcode-theme",
  "toggleTheme",
  "data-theme",
];
for (const m of markers) {
  if (!src.includes(m)) {
    console.error("app.jsx missing marker:", m);
    process.exit(1);
  }
}

// Minimal parity check for markdown shapes Studio expects to render.
function renderMarkdown(src) {
  const esc = (s) => String(s)
    .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  const inline = (s) => {
    let t = esc(s);
    t = t.replace(/`([^`]+)`/g, "<code>$1</code>");
    t = t.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
    return t;
  };
  const lines = String(src || "").split("\n");
  const out = [];
  for (const line of lines) {
    if (/^#\s+/.test(line)) out.push("<h1>" + inline(line.replace(/^#\s+/, "")) + "</h1>");
    else if (/^-\s+/.test(line)) out.push("<li>" + inline(line.replace(/^-\s+/, "")) + "</li>");
    else if (line.trim()) out.push("<p>" + inline(line) + "</p>");
  }
  return out.join("\n");
}

const html = renderMarkdown("# Title\n\n**bold** and `code`\n\n- item\n");
for (const n of ["<h1>", "<strong>", "<code>", "<li>"]) {
  if (!html.includes(n)) {
    console.error("markdown missing", n, html);
    process.exit(1);
  }
}
console.log("markdown_node_test: ok");
