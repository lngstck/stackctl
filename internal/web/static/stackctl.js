// stackctl · was learningstack.js nicht mitbringt. Ohne Abhängigkeiten.
//
// body[data-open="id"]     öffnet dieses Blatt beim Laden (/apps/{id})
// [data-close]             schließt den Dialog, in dem es steht
// button[data-busy]        zeigt nach dem Absenden einen Spinner, verhindert Doppelklick
// form[data-autosubmit]    sendet ab, sobald sich ein Schalter darin ändert
// [data-copy="text"]       kopiert den Text, auch ohne HTTPS
// .ls-split[data-panels]   zeigt nur den Bereich aus dem Anker (#konten)

(() => {
  // Blatt beim Laden öffnen, etwa nach einer Aktion in diesem Blatt.
  const openOnLoad = () => {
    const id = document.body.dataset.open;
    const d = id && document.getElementById(id);
    if (d && typeof d.showModal === 'function') d.showModal();
  };

  document.addEventListener('click', e => {
    const close = e.target.closest('[data-close]');
    if (close) { close.closest('dialog')?.close(); return; }

    const copy = e.target.closest('[data-copy]');
    if (copy) { e.preventDefault(); copyText(copy.dataset.copy).then(() => flag(copy), () => {}); }
  });

  // Spinner erst, wenn das Formular wirklich abgeschickt wird.
  document.addEventListener('submit', e => {
    const btn = e.submitter;
    if (!btn || !btn.matches('[data-busy]')) return;
    // Erst nach dem Absenden sperren, sonst fehlt der Knopf im Formular.
    setTimeout(() => { btn.setAttribute('aria-busy', 'true'); btn.disabled = true; }, 0);
  });
  // Zurück-Knopf des Browsers: Seite aus dem Cache darf nicht hängen bleiben.
  addEventListener('pageshow', ev => {
    if (!ev.persisted) return;
    document.querySelectorAll('[aria-busy="true"]').forEach(b => { b.removeAttribute('aria-busy'); b.disabled = false; });
  });

  document.addEventListener('change', e => {
    const form = e.target.closest('form[data-autosubmit]');
    if (form && e.target.matches('input[role="switch"], input[type="checkbox"]')) form.requestSubmit();
  });

  // navigator.clipboard gibt es nur über HTTPS oder localhost; stackctl
  // läuft oft unter http://<IP>:8090. Dann hilft der alte Weg.
  function copyText(text) {
    if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text);
    return new Promise((resolve, reject) => {
      const ta = document.createElement('textarea');
      ta.value = text; ta.setAttribute('readonly', '');
      ta.style.cssText = 'position:fixed;top:0;left:0;opacity:0';
      (document.querySelector('dialog[open]') || document.body).appendChild(ta);
      ta.select();
      try { document.execCommand('copy') ? resolve() : reject(new Error('copy')); }
      catch (err) { reject(err); }
      finally { ta.remove(); }
    });
  }
  window.stackctlCopy = copyText;
  function flag(btn) {
    const label = btn.getAttribute('aria-label');
    btn.dataset.copied = '';
    btn.setAttribute('aria-label', 'Kopiert');
    setTimeout(() => { delete btn.dataset.copied; btn.setAttribute('aria-label', label); }, 1500);
  }

  // Geteilte Ansicht: links Bereiche, rechts genau einer. Ohne JS stehen
  // alle untereinander und die Liste springt zu ihnen.
  const panels = () => {
    document.querySelectorAll('.ls-split[data-panels]').forEach(split => {
      const all = [...split.querySelectorAll('.ls-panel')];
      if (!all.length) return;
      const want = location.hash.slice(1) || split.dataset.default;
      const current = all.find(p => p.id === want) || all[0];
      all.forEach(p => { p.hidden = p !== current; });
      split.querySelectorAll(':scope > nav a.ls-row').forEach(a => {
        if (a.hash === '#' + current.id) a.setAttribute('aria-current', 'true');
        else a.removeAttribute('aria-current');
      });
    });
  };
  addEventListener('hashchange', () => { panels(); scrollTo({ top: 0 }); });

  // Der Anker (#konten) wählt nur den Bereich aus. Den Sprung dorthin, den
  // der Browser beim Laden macht, nehmen wir zurück: Der Seitenkopf bleibt.
  addEventListener('load', () => {
    const target = location.hash && document.getElementById(location.hash.slice(1));
    if (target && target.matches('.ls-split[data-panels] .ls-panel')) {
      setTimeout(() => scrollTo({ top: 0 }), 0);
    }
  });

  const ready = () => { panels(); openOnLoad(); };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', ready); else ready();
})();
