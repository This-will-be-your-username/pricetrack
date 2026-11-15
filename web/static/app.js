// Progressive enhancement: busy states and delete confirmation.
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

// While a background retailer search is running, poll by reloading.
if (document.body.hasAttribute('data-searching')) {
  setTimeout(() => location.reload(), 4000);
}
