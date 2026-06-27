// Farbschema-Steuerung.
//
// Standard folgt dem OS (color-scheme: light dark in stackctl.css). Der Admin
// kann per Topnav-Button auf Hell/Dunkel zwingen; die Wahl liegt in
// localStorage. Diese Datei wird SYNCHRON ganz oben im <head> geladen (vor dem
// CSS), damit eine erzwungene Wahl ohne Flash (FOUC) greift.
(function () {
  var KEY = 'stackctl-theme';
  var ORDER = ['auto', 'light', 'dark'];
  var ICON = { auto: '◐', light: '☀', dark: '☾' };
  var LABEL = { auto: 'System', light: 'Hell', dark: 'Dunkel' };

  function read() {
    try {
      var v = localStorage.getItem(KEY);
      return v === 'light' || v === 'dark' ? v : 'auto';
    } catch (e) {
      return 'auto';
    }
  }

  // 'auto' → leeres inline color-scheme, damit der CSS-Default (light dark)
  // greift und dem OS folgt. 'light'/'dark' erzwingen den Modus.
  function apply(mode) {
    document.documentElement.style.colorScheme = mode === 'auto' ? '' : mode;
  }

  // 1) Sofort anwenden (vor erstem Paint) — verhindert Flash.
  apply(read());

  // 2) Topnav-Button verkabeln, sobald das DOM steht.
  function wire() {
    var btn = document.getElementById('theme-toggle');
    if (!btn) return;
    var icon = btn.querySelector('.theme-toggle__icon');
    function render(mode) {
      if (icon) icon.textContent = ICON[mode];
      btn.title = 'Farbschema: ' + LABEL[mode];
      btn.setAttribute(
        'aria-label',
        'Farbschema: ' + LABEL[mode] + ' — klicken zum Wechseln'
      );
    }
    var mode = read();
    render(mode);
    btn.addEventListener('click', function () {
      mode = ORDER[(ORDER.indexOf(mode) + 1) % ORDER.length];
      try {
        localStorage.setItem(KEY, mode);
      } catch (e) {}
      apply(mode);
      render(mode);
      window.dispatchEvent(new Event('stackctl:themechange'));
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', wire);
  } else {
    wire();
  }
})();
