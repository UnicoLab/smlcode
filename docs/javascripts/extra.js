/* Small UX niceties for SLMCode docs */
document.addEventListener("DOMContentLoaded", () => {
  // Mark external links
  document.querySelectorAll('.md-typeset a[href^="http"]').forEach((a) => {
    if (!a.hostname.includes("unicolab.github.io") && !a.hostname.includes("localhost")) {
      a.setAttribute("rel", "noopener");
    }
  });
});
