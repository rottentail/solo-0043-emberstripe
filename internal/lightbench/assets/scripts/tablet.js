// Progressive enhancement for the server-rendered step-tablet report.
// The page is fully usable without this module (the profile form submits a
// normal GET); here switching calibration re-runs the evaluation immediately.
const select = document.querySelector('[data-profile-select]');
if (select) {
  select.addEventListener('change', () => {
    if (select.value) select.form.requestSubmit ? select.form.requestSubmit() : select.form.submit();
  });
}
