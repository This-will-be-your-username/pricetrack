// Progressive enhancement: busy states, delete confirmation, retailer panels.
document.addEventListener('submit', (e) => {
  const f = e.target;
  if (f.dataset.confirm && !confirm(f.dataset.confirm)) {
    e.preventDefault();
    return;
  }
  const b = f.querySelector('button[type=submit], button:not([type])');
  if (b && f.dataset.busy) {
    // Defer so the form data is still submitted.
    setTimeout(() => { b.disabled = true; b.textContent = f.dataset.busy; }, 0);
  }
});

// Drop ?msg= / ?err= from the address bar after showing the flash.
if (location.search) history.replaceState(null, '', location.pathname);

// Retailer panels open only via their chevron button. Open panels are
// remembered, so submitting a price or a page refresh doesn't collapse them.
const OPEN_KEY = 'pricetrack-open';
function loadOpen() {
  try { return new Set(JSON.parse(localStorage.getItem(OPEN_KEY) || '[]')); } catch { return new Set(); }
}
function saveOpen(set) {
  try { localStorage.setItem(OPEN_KEY, JSON.stringify([...set])); } catch { /* storage unavailable */ }
}
function setOpen(box, open) {
  const btn = box.querySelector('.toggle');
  box.querySelector('.rpanel').hidden = !open;
  btn.setAttribute('aria-expanded', open ? 'true' : 'false');
  const label = open ? 'Hide retailers' : 'Show retailers';
  btn.title = label;
  btn.setAttribute('aria-label', label);
}
const openIds = loadOpen();
document.querySelectorAll('.retailers-box').forEach((box) => setOpen(box, openIds.has(box.dataset.item)));
document.addEventListener('click', (e) => {
  const btn = e.target.closest('.toggle');
  if (!btn) return;
  const box = btn.closest('.retailers-box');
  const open = btn.getAttribute('aria-expanded') !== 'true';
  setOpen(box, open);
  const ids = loadOpen();
  if (open) ids.add(box.dataset.item); else ids.delete(box.dataset.item);
  saveOpen(ids);
});

// While a background retailer search is running, poll by reloading — but never
// while the user is typing in a box.
function poll() {
  const a = document.activeElement;
  const typing = a && (a.tagName === 'INPUT' || a.tagName === 'TEXTAREA') && a.value !== '';
  if (typing) { setTimeout(poll, 4000); return; }
  location.reload();
}
if (document.body.hasAttribute('data-searching')) {
  setTimeout(poll, 4000);
}

// Pencil button reveals the price box for that retailer; it stays hidden otherwise.
document.addEventListener('click', (e) => {
  const edit = e.target.closest('.edit');
  if (!edit) return;
  const form = edit.closest('li').querySelector('.pform');
  form.hidden = !form.hidden;
  if (!form.hidden) form.querySelector('input').focus();
});
