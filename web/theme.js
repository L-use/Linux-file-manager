// Apply the stored appearance before the page styles can paint.
try {
  if (localStorage.getItem('fm-theme') === 'night') {
    document.documentElement.dataset.theme = 'night';
    document.querySelector('meta[name="theme-color"]').content = '#08110c';
  }
} catch {
  // The page can still use its default appearance if storage is unavailable.
}
