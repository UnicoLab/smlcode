/* Small UX niceties for SLMCode docs — keep it light, keep it fun */
document.addEventListener("DOMContentLoaded", () => {
  document.querySelectorAll('.md-typeset a[href^="http"]').forEach((a) => {
    const host = a.hostname || "";
    if (!host.includes("unicolab.github.io") && !host.includes("localhost")) {
      a.setAttribute("rel", "noopener");
      if (!a.classList.contains("md-button") && !a.querySelector("img")) {
        a.classList.add("slm-ext");
      }
    }
  });

  // Celebrate copy buttons a tiny bit
  document.querySelectorAll(".md-clipboard").forEach((btn) => {
    btn.addEventListener("click", () => {
      const original = btn.getAttribute("title") || "Copy";
      btn.setAttribute("title", "Copied — you're dangerous now ✨");
      setTimeout(() => btn.setAttribute("title", original), 1600);
    });
  });
});
