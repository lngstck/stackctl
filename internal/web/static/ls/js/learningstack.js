// learningstack · kleines Hilfsskript (optional, ohne Abhängigkeiten)
// <script src="/static/ls/js/learningstack.js" defer></script>
//
// data-ls-open="id"        öffnet <dialog id="id"> (Blatt oder Dialog)
// Klick auf den Hintergrund schließt ein offenes Blatt
// data-ls-theme="light|dark|system"  Modus wählen, wird gemerkt
// input[data-ls-filter]    filtert [data-ls-item] auf der Seite, blendet leere Regale aus
// .ls-seg[data-ls-switch="#ziel"]  schaltet data-view am Ziel um (z. B. Raster/Liste)
// Taste „/“ springt in das erste Suchfeld
// Am Handy lässt sich ein Blatt am Griff nach unten wegziehen

(() => {
  const root = document.documentElement;
  const store = {
    get: k => { try { return localStorage.getItem(k); } catch { return null; } },
    set: (k, v) => { try { v == null ? localStorage.removeItem(k) : localStorage.setItem(k, v); } catch { /* privat */ } },
  };

  const applyTheme = t => {
    if (t === 'light' || t === 'dark') root.dataset.theme = t; else delete root.dataset.theme;
    document.querySelectorAll('[data-ls-theme]').forEach(b => b.setAttribute('aria-pressed', String((b.dataset.lsTheme === (t || 'system')))));
  };
  applyTheme(store.get('ls-theme'));

  document.addEventListener('click', e => {
    const open = e.target.closest('[data-ls-open]');
    if (open) {
      const d = document.getElementById(open.dataset.lsOpen);
      if (d && typeof d.showModal === 'function') { e.preventDefault(); d.showModal(); }
      return;
    }
    const theme = e.target.closest('[data-ls-theme]');
    if (theme) {
      const t = theme.dataset.lsTheme === 'system' ? null : theme.dataset.lsTheme;
      store.set('ls-theme', t); applyTheme(t);
      return;
    }
    const seg = e.target.closest('.ls-seg[data-ls-switch] > button');
    if (seg) {
      const group = seg.parentElement;
      group.querySelectorAll('button').forEach(b => b.setAttribute('aria-pressed', String(b === seg)));
      const target = document.querySelector(group.dataset.lsSwitch);
      if (target) target.dataset.view = seg.value;
      return;
    }
    // Hintergrund eines offenen Blatts oder Dialogs
    if (e.target instanceof HTMLDialogElement && e.target.open) {
      const r = e.target.getBoundingClientRect();
      const inside = e.clientX >= r.left && e.clientX <= r.right && e.clientY >= r.top && e.clientY <= r.bottom;
      if (!inside) e.target.close();
    }
  });

  document.addEventListener('input', e => {
    const input = e.target.closest('input[data-ls-filter]');
    if (!input) return;
    const q = input.value.trim().toLowerCase();
    document.querySelectorAll('[data-ls-item]').forEach(el => {
      el.hidden = q !== '' && !el.textContent.toLowerCase().includes(q) && !(el.dataset.lsItem || '').toLowerCase().includes(q);
    });
    document.querySelectorAll('.ls-shelf').forEach(s => { s.hidden = !s.querySelector('[data-ls-item]:not([hidden])'); });
    const empty = document.querySelector('[data-ls-empty]');
    if (empty) empty.hidden = !!document.querySelector('[data-ls-item]:not([hidden])');
  });

  // Am Handy: Blatt am Griff nach unten wegziehen
  const narrow = matchMedia('(max-width: 40rem)');
  document.addEventListener('pointerdown', e => {
    const bar = e.target.closest('dialog.ls-sheet[open] .ls-sheet-bar');
    if (!bar || !narrow.matches || e.target.closest('button')) return;
    const sheet = bar.closest('dialog');
    const y0 = e.clientY; let dy = 0;
    sheet.style.transition = 'none';
    const move = ev => { dy = Math.max(0, ev.clientY - y0); sheet.style.transform = `translateY(${dy}px)`; };
    const up = () => {
      removeEventListener('pointermove', move); removeEventListener('pointerup', up); removeEventListener('pointercancel', up);
      sheet.style.transition = ''; sheet.style.transform = '';
      if (dy > 90) sheet.close();
    };
    addEventListener('pointermove', move); addEventListener('pointerup', up); addEventListener('pointercancel', up);
  });

  // Waagerecht scrollende Bereichsliste: Ausblendung am Ende entfernen
  const fade = el => el.classList.toggle('is-end', el.scrollLeft + el.clientWidth >= el.scrollWidth - 2);
  const fadeAll = () => document.querySelectorAll('.ls-split > nav .ls-list').forEach(fade);
  document.addEventListener('scroll', e => { if (e.target instanceof Element && e.target.matches('.ls-split > nav .ls-list')) fade(e.target); }, true);
  addEventListener('resize', fadeAll);
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', fadeAll); else fadeAll();

  document.addEventListener('keydown', e => {
    if (e.key !== '/' || e.metaKey || e.ctrlKey || e.altKey) return;
    const tag = document.activeElement?.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || document.activeElement?.isContentEditable) return;
    const search = document.querySelector('.ls-search input');
    if (search) { e.preventDefault(); search.focus(); }
  });
})();
