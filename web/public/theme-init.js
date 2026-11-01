// Stamp the theme before React renders, so there is no flash of the wrong
// palette. Mirrors applyTheme() in src/hooks/useTheme.ts: an explicit
// choice stamps .dark/.light; 'system' stamps nothing and lets the
// prefers-color-scheme media query in index.css decide.
// New visits and invalid preferences default to dark, matching lib/theme.ts.
//
// This lives in its own file (rather than an inline <script> in index.html)
// because the CSP's script-src is 'self' with no 'unsafe-inline': an inline
// script would be blocked.
(function () {
  var theme = 'dark';
  try {
    var saved = localStorage.getItem('opskest-theme');
    if (saved === 'light' || saved === 'dark' || saved === 'system') theme = saved;
  } catch (e) { /* Storage may be blocked; keep the dark default. */ }
  var root = document.documentElement;
  root.classList.remove('dark', 'light');
  if (theme !== 'system') root.classList.add(theme);
})();
