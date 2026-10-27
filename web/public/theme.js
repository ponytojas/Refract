// Apply the theme before first paint (no light flash in dark mode).
try {
  var t = localStorage.getItem('refract-theme')
  if (t === 'dark' || (t !== 'light' && matchMedia('(prefers-color-scheme: dark)').matches)) document.documentElement.classList.add('dark')
} catch (e) {}
