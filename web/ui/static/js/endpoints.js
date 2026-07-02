// Endpoint form/list behavior: provider-select drives which fields show
// (DESIGN.md §6.2), and delete requires typing the endpoint's path to
// confirm before the DELETE request is sent.
(function () {
  var FIELDS_BY_PROVIDER = {
    stripe: ["secret", "replay"],
    github: ["secret"],
    shopify: ["secret"],
    paypal: ["webhook"],
  };

  function applyProviderVisibility(select) {
    var visible = FIELDS_BY_PROVIDER[select.value] || [];
    document.querySelectorAll("[data-provider-field]").forEach(function (el) {
      var key = el.getAttribute("data-provider-field");
      el.style.display = visible.indexOf(key) === -1 ? "none" : "";
    });
  }

  var select = document.querySelector("[data-provider-select]");
  if (select) {
    select.addEventListener("change", function () {
      applyProviderVisibility(select);
    });
  }

  document.addEventListener("click", function (e) {
    var btn = e.target.closest("[data-delete-endpoint]");
    if (!btn) return;

    var path = btn.getAttribute("data-endpoint-path");
    var typed = window.prompt('Type the endpoint path "' + path + '" to confirm deletion:');
    if (typed !== path) {
      return;
    }

    var id = btn.getAttribute("data-endpoint-id");
    var csrf = btn.getAttribute("data-csrf-token");
    fetch("/dashboard/endpoints/" + id, {
      method: "DELETE",
      headers: { "X-CSRF-Token": csrf },
    }).then(function () {
      window.location.href = "/dashboard/endpoints";
    });
  });
})();
