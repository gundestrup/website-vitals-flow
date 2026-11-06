// theme: auto -> light -> dark cycle, localStorage-persisted
// runs head-blocking so data-theme is set before first paint
(function () {
  const KEY = 'wvf-theme';
  const get = () => localStorage.getItem(KEY) || 'auto';
  const apply = (m) => {
    const t = m === 'auto'
      ? (matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light')
      : m;
    document.documentElement.dataset.theme = t;
  };
  apply(get());
  window.wvfTheme = {
    get, apply,
    cycle() {
      const order = ['auto', 'light', 'dark'];
      const next = order[(order.indexOf(get()) + 1) % order.length];
      localStorage.setItem(KEY, next);
      apply(next);
      return next;
    },
    resolved() { return document.documentElement.dataset.theme; },
  };
  matchMedia('(prefers-color-scheme: dark)')
    .addEventListener('change', () => { if (get() === 'auto') apply('auto'); });
})();
