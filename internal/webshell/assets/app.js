"use strict";

const token = document.querySelector('meta[name="errkb-token"]').content;
const input = document.getElementById("query");
const status = document.getElementById("status");
const hits = document.getElementById("hits");
let sequence = 0;

function escapeText(value) {
  const node = document.createElement("span");
  node.textContent = value;
  return node.innerHTML;
}

function setStatus(text, isError) {
  status.textContent = text;
  status.className = isError ? "error" : "";
}

function renderEmpty() {
  hits.innerHTML =
    '<li class="empty">No matching issue yet. Record it first: <code>errkb record</code> — or capture the failure with <code>errkb run -- &lt;command&gt;</code>.</li>';
}

function renderHits(candidates) {
  hits.innerHTML = "";
  if (!candidates || candidates.length === 0) {
    renderEmpty();
    return;
  }
  for (const candidate of candidates.slice(0, 3)) {
    const item = document.createElement("li");
    item.className = "hit";
    const badge = candidate.approximate
      ? "APPROXIMATE — confirm before reuse"
      : "exact";
    const badgeClass = candidate.approximate ? "approximate" : "exact";
    item.innerHTML =
      '<h2><a href="/issues/' +
      encodeURIComponent(candidate.issue_id) +
      '">' +
      escapeText(candidate.title) +
      "</a></h2>" +
      '<div class="meta">' +
      '<span class="badge ' +
      badgeClass +
      '">' +
      badge +
      "</span>" +
      "<span>" +
      escapeText(candidate.match_reason) +
      "</span>" +
      "<span>issue " +
      escapeText(candidate.issue_id) +
      "</span>" +
      "<span>fingerprint " +
      escapeText(candidate.fingerprint) +
      "</span>" +
      (typeof candidate.hits === "number"
        ? "<span>" + candidate.hits + " hit(s)</span>"
        : "") +
      "</div>" +
      (candidate.match_detail
        ? '<p class="reason">' + escapeText(candidate.match_detail) + "</p>"
        : "") +
      (candidate.solution && candidate.solution.body
        ? '<pre class="fix">' + escapeText(candidate.solution.body) + "</pre>"
        : "");
    hits.appendChild(item);
  }
}

async function search() {
  const query = input.value.trim();
  if (query === "") {
    hits.innerHTML = "";
    setStatus("");
    return;
  }
  const ticket = ++sequence;
  setStatus("searching…");
  try {
    const response = await fetch("/api/search", {
      method: "POST",
      headers: { "content-type": "application/json", "x-errkb-token": token },
      body: JSON.stringify({ query }),
    });
    if (ticket !== sequence) return;
    if (!response.ok) {
      const payload = await response.json().catch(() => ({}));
      setStatus(payload.message || "search failed", true);
      return;
    }
    const payload = await response.json();
    const candidates = payload.candidates || [];
    setStatus(
      candidates.length === 0
        ? ""
        : candidates.length + " candidate(s), newest relevance first",
    );
    renderHits(candidates);
  } catch (error) {
    if (ticket === sequence) setStatus("the errkb core is not reachable", true);
  }
}

let timer;
input.addEventListener("input", () => {
  clearTimeout(timer);
  timer = setTimeout(search, 180);
});
input.addEventListener("keydown", (event) => {
  if (event.key === "Enter") {
    clearTimeout(timer);
    search();
  }
});

// ---- detail page (WF-005) -------------------------------------------------
function environmentText(environment) {
  return Object.keys(environment || {})
    .sort()
    .map((key) => key + "=" + environment[key])
    .join(", ");
}

function renderVersions(solutions) {
  const versions = [];
  for (const solution of solutions || []) {
    for (const version of solution.versions || []) {
      versions.push({ status: solution.status, version });
    }
  }
  versions.sort(
    (left, right) => (right.version.version || 0) - (left.version.version || 0),
  );
  if (versions.length === 0) return "<p>no solution recorded yet</p>";
  return versions
    .map((entry) => {
      const version = entry.version;
      const copy = [version.body || ""];
      if (version.source) copy.push("source: " + version.source);
      if (version.applies_to) copy.push("applies to: " + version.applies_to);
      if (version.risks) copy.push("risks: " + version.risks);
      return (
        '<article class="version">' +
        "<h3>v" +
        version.version +
        " · " +
        escapeText(entry.status) +
        "</h3>" +
        "<pre>" +
        escapeText(version.body) +
        "</pre>" +
        '<p class="meta">' +
        (version.source
          ? "<span>source " + escapeText(version.source) + "</span>"
          : "") +
        (version.applies_to
          ? "<span>applies to " + escapeText(version.applies_to) + "</span>"
          : "") +
        (version.risks
          ? "<span>risks " + escapeText(version.risks) + "</span>"
          : "") +
        (version.preconditions
          ? "<span>preconditions " +
            escapeText(version.preconditions) +
            "</span>"
          : "") +
        "</p>" +
        '<button type="button" data-copy="' +
        escapeText(copy.join("\n")) +
        '">Copy fix</button>' +
        "<p>" +
        '<button type="button" data-solution="' +
        escapeText(entry.solution.id) +
        '" data-solution-action="status" data-solution-status="verified">Verify</button>' +
        '<button type="button" data-solution="' +
        escapeText(entry.solution.id) +
        '" data-solution-action="status" data-solution-status="deprecated" data-confirmed="true">Deprecate</button>' +
        '<button type="button" data-solution="' +
        escapeText(entry.solution.id) +
        '" data-solution-action="delete">Delete solution</button>' +
        "</p>" +
        "</article>"
      );
    })
    .join("");
}

function renderDetail(detail) {
  const counts = { success: 0, failure: 0, unknown: 0 };
  for (const outcome of detail.outcomes || []) {
    if (counts[outcome.outcome] !== undefined) counts[outcome.outcome] += 1;
  }
  const environmentRows = (detail.occurrences || [])
    .map((occurrence) => {
      const text = environmentText(occurrence.environment);
      return (
        "<li>" +
        escapeText(occurrence.headline || occurrence.id) +
        (text ? ' <span class="env">' + escapeText(text) + "</span>" : "") +
        "</li>"
      );
    })
    .join("");
  const trust = detail.trust || {};
  return (
    '<p><a href="/">← back to search</a></p>' +
    "<h2>" +
    escapeText(detail.title) +
    "</h2>" +
    '<p class="meta"><span>issue ' +
    escapeText(detail.id) +
    "</span>" +
    "<span>fingerprint " +
    escapeText(detail.fingerprint) +
    "</span>" +
    "<span>" +
    escapeText(detail.status) +
    "</span>" +
    "<span>" +
    detail.hits +
    " hit(s)</span></p>" +
    '<p class="reason">trust ' +
    Number(trust.score || 0).toFixed(2) +
    " (based on " +
    Number(trust.samples || 0) +
    " recorded outcome(s); read-only)</p>" +
    "<h3>Environment</h3><ul>" +
    (environmentRows || "<li>not recorded</li>") +
    "</ul>" +
    "<h3>Solution history (newest first)</h3>" +
    renderVersions(detail.solutions) +
    "<h3>Outcomes</h3><p>success " +
    counts.success +
    " · failure " +
    counts.failure +
    " · unknown " +
    counts.unknown +
    "</p>"
  );
}

function preventionVerification(version) {
  const verification = version.latest_verification;
  if (!verification) return "";
  const outcome = verification.passed ? "passed" : "failed";
  const exitCode =
    typeof verification.exit_code === "number"
      ? " · exit " + escapeText(String(verification.exit_code))
      : "";
  return (
    '<p class="meta">latest verification ' +
    escapeText(outcome) +
    " · " +
    escapeText(verification.created_at || "not recorded") +
    exitCode +
    (verification.command
      ? " · command " + escapeText(verification.command.join(" "))
      : "") +
    "</p>"
  );
}

function preventionCLIHint(version) {
  const id = escapeText(version.artifact_version_id);
  if (version.status === "draft") {
    return (
      '<p class="reason">Verify locally: <code>errkb prevention verify ' +
      id +
      " --repo &lt;repository&gt; -- &lt;command&gt;</code></p>"
    );
  }
  if (version.status === "verified") {
    return (
      '<p class="reason">Activate locally: <code>errkb prevention activate ' +
      id +
      " --repo &lt;repository&gt;</code></p>"
    );
  }
  if (version.status === "deprecated") {
    return '<p class="meta">Historical version; no action available.</p>';
  }
  return "";
}

function renderPreventions(result) {
  const opening = '<section class="preventions"><h3>Preventions</h3>';
  if (result.status === "rejected") {
    const message =
      result.reason && result.reason.message
        ? result.reason.message
        : "could not load preventions";
    return (
      opening + '<p class="error">' + escapeText(message) + "</p></section>"
    );
  }
  const items = (result.value && result.value.items) || [];
  if (items.length === 0) {
    return opening + "<p>no preventions recorded yet</p></section>";
  }
  return (
    opening +
    items
      .map((item) => {
        const artifact = item.artifact || {};
        const versions = (item.versions || [])
          .slice()
          .sort((left, right) => (right.version || 0) - (left.version || 0));
        return (
          '<article class="version prevention">' +
          "<h3>" +
          escapeText(artifact.kind || "unknown") +
          " · " +
          escapeText(artifact.title || "untitled") +
          "</h3>" +
          versions
            .map((version) => {
              const active =
                version.status === "active"
                  ? '<p class="meta">HEAD ' +
                    escapeText(version.commit_sha || "not recorded") +
                    " · activated " +
                    escapeText(version.activated_at || "not recorded") +
                    "</p>"
                  : "";
              return (
                '<section class="prevention-version">' +
                "<h4>v" +
                escapeText(String(Number(version.version || 0))) +
                " · " +
                escapeText(version.status || "unknown") +
                "</h4>" +
                '<p class="meta">path <code>' +
                escapeText(version.artifact_path || "not recorded") +
                "</code></p>" +
                preventionVerification(version) +
                active +
                preventionCLIHint(version) +
                "</section>"
              );
            })
            .join("") +
          "</article>"
        );
      })
      .join("") +
    "</section>"
  );
}

async function loadDetail() {
  const match = location.pathname.match(/^\/issues\/([^/]+)$/);
  if (!match) return;
  const issueID = decodeURIComponent(match[1]);
  const main = document.querySelector("main");
  main.innerHTML = '<p id="status" role="status">loading…</p>';
  const read = async (url) => {
    const response = await fetch(url, {
      headers: { "x-errkb-token": token },
    });
    const payload = await response.json().catch(() => ({}));
    if (!response.ok) {
      throw new Error(payload.message || "the errkb core is not reachable");
    }
    return payload;
  };
  try {
    const [detailResult, preventionResult] = await Promise.allSettled([
      read("/api/issues/" + encodeURIComponent(issueID)),
      read("/api/issues/" + encodeURIComponent(issueID) + "/preventions"),
    ]);
    if (detailResult.status === "rejected") {
      const message =
        detailResult.reason && detailResult.reason.message
          ? detailResult.reason.message
          : "could not load the issue";
      main.innerHTML =
        '<p class="error">' +
        escapeText(message) +
        "</p>" +
        '<p><a href="/">back to search</a></p>';
      return;
    }
    main.innerHTML = renderDetail(detailResult.value.issue || {});
    main.insertAdjacentHTML("beforeend", renderPreventions(preventionResult));
    for (const button of main.querySelectorAll("[data-copy]")) {
      button.addEventListener("click", async () => {
        try {
          await navigator.clipboard.writeText(button.getAttribute("data-copy"));
          button.textContent = "Copied";
        } catch (error) {
          button.textContent = "Copy failed";
        }
      });
    }
    main.insertAdjacentHTML("beforeend", editSection());
    wireEdit(issueID, main);
  } catch (error) {
    main.innerHTML = '<p class="error">the errkb core is not reachable</p>';
  }
}

function editSection() {
  return (
    '<section class="edit">' +
    "<h3>Record a fix</h3>" +
    '<form id="version-form">' +
    '<label>What fixes it? <textarea id="version-body" required></textarea></label>' +
    '<label>Preconditions <input id="version-preconditions" /></label>' +
    '<label>Source <input id="version-source" value="toolbox" /></label>' +
    '<button type="submit">Add version</button>' +
    "</form>" +
    "<h3>Issue status</h3>" +
    '<p class="status-buttons">' +
    '<button type="button" data-status="resolved">Mark resolved</button>' +
    '<button type="button" data-status="open">Mark open</button>' +
    '<button type="button" data-status="wontfix">Mark wontfix</button>' +
    '<button type="button" data-status="misjudged">Mark misjudged</button>' +
    "</p>" +
    '<p id="edit-status" role="status"></p>' +
    "</section>"
  );
}

function wireEdit(issueID, main) {
  const status = main.querySelector("#edit-status");
  const say = (text, isError) => {
    status.textContent = text;
    status.className = isError ? "error" : "";
  };
  const form = main.querySelector("#version-form");
  if (form) {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      say("saving…");
      try {
        const response = await fetch(
          "/api/issues/" + encodeURIComponent(issueID) + "/versions",
          {
            method: "POST",
            headers: {
              "content-type": "application/json",
              "x-errkb-token": token,
            },
            body: JSON.stringify({
              body: main.querySelector("#version-body").value,
              preconditions: main.querySelector("#version-preconditions").value,
              source: main.querySelector("#version-source").value,
            }),
          },
        );
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          say(payload.message || "the daemon refused the write", true);
          return;
        }
        location.reload();
      } catch (error) {
        say("the errkb core is not reachable", true);
      }
    });
  }
  for (const button of main.querySelectorAll("[data-solution-action]")) {
    button.addEventListener("click", async () => {
      const solutionID = button.getAttribute("data-solution");
      const action = button.getAttribute("data-solution-action");
      let url = "/api/solutions/" + encodeURIComponent(solutionID);
      let body;
      if (action === "delete") {
        if (button.getAttribute("data-preview") !== "1") {
          button.setAttribute("data-preview", "1");
          button.textContent = "Confirm delete";
          const note = document.createElement("p");
          note.className = "warning";
          note.textContent =
            "This removes the solution and all of its version history. Click Confirm delete to continue.";
          button.after(note);
          return;
        }
        url += "/delete";
        body = JSON.stringify({ confirmed: true });
      } else {
        url += "/status";
        body = JSON.stringify({
          status: button.getAttribute("data-solution-status"),
          confirmed: button.getAttribute("data-confirmed") === "true",
        });
      }
      say("saving…");
      try {
        const response = await fetch(url, {
          method: "POST",
          headers: {
            "content-type": "application/json",
            "x-errkb-token": token,
          },
          body: body,
        });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          say(payload.message || "the daemon refused the write", true);
          return;
        }
        location.reload();
      } catch (error) {
        say("the errkb core is not reachable", true);
      }
    });
  }
  for (const button of main.querySelectorAll("[data-status]")) {
    button.addEventListener("click", async () => {
      say("saving…");
      try {
        const response = await fetch(
          "/api/issues/" + encodeURIComponent(issueID) + "/status",
          {
            method: "POST",
            headers: {
              "content-type": "application/json",
              "x-errkb-token": token,
            },
            body: JSON.stringify({
              status: button.getAttribute("data-status"),
            }),
          },
        );
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) {
          say(payload.message || "the daemon refused the write", true);
          return;
        }
        location.reload();
      } catch (error) {
        say("the errkb core is not reachable", true);
      }
    });
  }
}

// ---- shared helpers (EKB-059) --------------------------------------------
async function fetchJSON(url, options) {
  const response = await fetch(
    url,
    Object.assign({ headers: { "x-errkb-token": token } }, options || {}),
  );
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.message || "the errkb core refused the request");
  }
  return payload;
}

function postJSON(url, body) {
  return fetchJSON(url, {
    method: "POST",
    headers: { "content-type": "application/json", "x-errkb-token": token },
    body: JSON.stringify(body),
  });
}

function number(value) {
  return Number(value || 0);
}

function percent(part, whole) {
  return whole ? ((number(part) / number(whole)) * 100).toFixed(1) + "%" : "–";
}

function milliseconds(value) {
  return number(value).toFixed(1) + " ms";
}

function formatTime(value) {
  if (!value) return "not recorded";
  const parsed = new Date(value);
  return Number.isNaN(parsed.getTime())
    ? escapeText(String(value))
    : parsed.toLocaleString();
}

function mainElement() {
  return document.querySelector("main");
}

// ---- statistics and audit (EKB-059) --------------------------------------
function metricRow(label, value, note, isAlert) {
  return (
    '<tr class="' +
    (isAlert ? "danger" : "") +
    '">' +
    "<th>" +
    label +
    "</th>" +
    '<td class="' +
    (isAlert ? "danger" : "") +
    '">' +
    escapeText(String(value)) +
    "</td>" +
    "<td>" +
    escapeText(note || "") +
    "</td>" +
    "</tr>"
  );
}

function renderStatsPage(stats) {
  const retrieval = stats.Retrieval || {};
  const solutions = stats.Solutions || {};
  const injections = stats.Injections || {};
  const repeats = stats.Repeats || {};
  const latency = stats.Latency || {};
  const corrections = stats.Corrections || {};
  const faults = stats.Faults || {};
  const savings = stats.Savings || {};
  const falseMerges = number(retrieval.FalseMerges);
  return (
    '<p class="meta"><span>generated ' +
    formatTime(stats.GeneratedAt) +
    "</span></p>" +
    "<h2>Core metrics</h2>" +
    '<table class="metrics">' +
    metricRow(
      "False merges (target: 0)",
      falseMerges,
      falseMerges === 0
        ? "no approximate row was reused as exact"
        : "a wrong merge happened — investigate before trusting recall",
      falseMerges > 0,
    ) +
    metricRow(
      "Retrieval rounds",
      number(retrieval.Rounds),
      "grouped retrieval calls",
    ) +
    metricRow(
      "Exact fingerprint hits",
      number(retrieval.ExactTopOne),
      "top-1 exact match",
    ) +
    metricRow(
      "Approximate top-1 precision",
      percent(retrieval.ApproximateTopOneHits, retrieval.ApproximateTopOneRuns),
      "confirmed by a recorded success outcome; a lower bound",
    ) +
    metricRow(
      "Approximate top-3 precision",
      percent(
        retrieval.ApproximateTopThreeHits,
        retrieval.ApproximateTopThreeRuns,
      ),
      "confirmed by a recorded success outcome; a lower bound",
    ) +
    metricRow(
      "Retrieval latency p50 / p95",
      milliseconds(retrieval.P50Milliseconds) +
        " / " +
        milliseconds(retrieval.P95Milliseconds),
      "measured over " +
        number(retrieval.LatencySamples) +
        " sample(s); the 150 ms gate applies on a 100k-issue dataset",
    ) +
    metricRow(
      "Solution successes / failures",
      number(solutions.Success) + " / " + number(solutions.Failure),
      "unknown outcomes: " + number(solutions.Unknown),
    ) +
    metricRow(
      "Success rate",
      percent(
        solutions.Success,
        number(solutions.Success) + number(solutions.Failure),
      ),
      "successes over decided outcomes",
    ) +
    metricRow(
      "Injected text",
      number(injections.Characters) + " characters",
      number(injections.Notices) + " notice(s) returned to the host",
    ) +
    metricRow(
      "Repeated diagnoses",
      number(repeats.RepeatedHits),
      "across " +
        number(repeats.RepeatedIssues) +
        " issue(s) seen more than once",
    ) +
    metricRow(
      "Capture-to-notice latency p50",
      milliseconds(latency.P50Milliseconds),
      "measured over " + number(latency.Samples) + " notice(s)",
    ) +
    metricRow(
      "Manual corrections",
      number(corrections.Total),
      (corrections.ByAction || [])
        .map((row) => row.Action + " " + row.Count)
        .join(", ") || "none recorded",
    ) +
    metricRow(
      "Persisted faults",
      number(faults.Total),
      (faults.ByAction || [])
        .map((row) => row.Action + " " + row.Count)
        .join(", ") || "none; queue and connector faults have no table yet",
    ) +
    "</table>" +
    "<h2>Savings</h2>" +
    '<p class="meta measured">measured · ' +
    number(injections.Characters) +
    " characters (" +
    number(injections.EstimatedTokens) +
    " tokens, estimated size) were handed back instead of re-explained</p>" +
    '<p class="meta estimated">estimated · ~' +
    number(savings.AvoidedTokens) +
    " tokens across " +
    number(savings.AvoidedDiagnoses) +
    " avoided diagnosis(es)</p>" +
    '<p class="reason">' +
    escapeText(savings.Assumption || "assumption not recorded") +
    "</p>" +
    "<h2>Audit trail</h2>" +
    '<form id="audit-filter" class="filters">' +
    '<label>Action <input id="audit-action" placeholder="delete.scope" /></label>' +
    '<label>From <input id="audit-from" type="datetime-local" /></label>' +
    '<label>To <input id="audit-to" type="datetime-local" /></label>' +
    '<label>Limit <input id="audit-limit" type="number" min="1" max="200" value="50" /></label>' +
    '<button type="submit">Filter</button>' +
    "</form>" +
    '<p id="audit-status" role="status"></p>' +
    '<table class="trail"><thead><tr><th>time</th><th>action</th><th>target</th><th>operator</th><th>detail</th></tr></thead><tbody id="audit-rows"></tbody></table>' +
    '<p id="audit-page" class="meta"></p>'
  );
}

function renderAuditPage(page) {
  const rows = page.Records || [];
  const tbody = document.querySelector("#audit-rows");
  tbody.innerHTML =
    rows
      .map((row) => {
        const detail = row.Detail ? JSON.stringify(row.Detail) : "";
        return (
          "<tr><td>" +
          formatTime(row.OccurredAt) +
          "</td>" +
          "<td>" +
          escapeText(row.Action) +
          "</td>" +
          "<td>" +
          escapeText(row.Entity + " " + row.EntityID) +
          "</td>" +
          "<td>" +
          escapeText(row.Operator || row.Source) +
          "</td>" +
          '<td class="detail">' +
          escapeText(detail) +
          "</td></tr>"
        );
      })
      .join("") || '<tr><td colspan="5">no matching audit rows</td></tr>';
  document.querySelector("#audit-page").textContent =
    "showing " +
    rows.length +
    " of " +
    number(page.Total) +
    " row(s) · limit " +
    number(page.Limit) +
    " · offset " +
    number(page.Offset);
}

async function loadAudit() {
  const status = document.querySelector("#audit-status");
  const params = new URLSearchParams();
  const action = document.querySelector("#audit-action").value.trim();
  const from = document.querySelector("#audit-from").value;
  const to = document.querySelector("#audit-to").value;
  const limit = document.querySelector("#audit-limit").value;
  if (action) params.set("action", action);
  if (from) params.set("from", new Date(from).toISOString());
  if (to) params.set("to", new Date(to).toISOString());
  if (limit) params.set("limit", limit);
  status.textContent = "loading…";
  status.className = "";
  try {
    renderAuditPage(await fetchJSON("/api/audit?" + params.toString()));
    status.textContent = "";
  } catch (error) {
    status.textContent = error.message;
    status.className = "error";
  }
}

async function loadStatsPage() {
  const main = mainElement();
  main.innerHTML = '<p id="status" role="status">loading…</p>';
  try {
    const payload = await fetchJSON("/api/stats");
    main.innerHTML = renderStatsPage(payload.stats || {});
    const form = document.querySelector("#audit-filter");
    if (form)
      form.addEventListener("submit", (event) => {
        event.preventDefault();
        loadAudit();
      });
    await loadAudit();
  } catch (error) {
    main.innerHTML = '<p class="error">' + escapeText(error.message) + "</p>";
  }
}

// ---- settings (EKB-059) --------------------------------------------------
function renderSettingsPage(view) {
  const capture = view.Capture || {};
  const retention = view.Retention || {};
  const http = view.HTTP || {};
  const pipe = view.Pipe || {};
  const connectors =
    (view.connectors || [])
      .map(
        (connector) =>
          "<tr><td>" +
          escapeText(connector.name) +
          "</td>" +
          "<td>" +
          escapeText(connector.channel) +
          "</td>" +
          "<td>" +
          escapeText(connector.version || "unknown") +
          "</td>" +
          "<td>" +
          formatTime(connector.last_active) +
          "</td>" +
          '<td><button type="button" class="toggle" data-connector="' +
          escapeText(connector.name) +
          '" data-enabled="' +
          (connector.enabled ? "1" : "0") +
          '">' +
          (connector.enabled ? "Disable" : "Enable") +
          "</button></td></tr>",
      )
      .join("") ||
    '<tr><td colspan="5">no connector has recorded an event yet</td></tr>';
  const pending = view.pending_deletion;
  return (
    '<p class="meta"><span>' +
    escapeText(view.status || "") +
    "</span></p>" +
    '<section class="settings"><h2>Capture</h2>' +
    "<p>Global capture is <strong>" +
    (capture.enabled ? "on" : "off") +
    "</strong>. " +
    '<button type="button" id="capture-toggle" data-enabled="' +
    (capture.enabled ? "1" : "0") +
    '">' +
    (capture.enabled ? "Pause capture" : "Resume capture") +
    "</button></p>" +
    "<p>Paused projects: " +
    escapeText((capture.paused_projects || []).join(", ") || "none") +
    "</p>" +
    '<p><label>Pause a project <input id="project-pause" placeholder="project key" /></label> ' +
    '<button type="button" id="project-pause-apply">Pause</button></p>' +
    "<h2>Connectors</h2>" +
    '<table class="connectors"><thead><tr><th>connector</th><th>channel</th><th>version</th><th>last active</th><th></th></tr></thead><tbody>' +
    connectors +
    "</tbody></table>" +
    "<h2>Retention</h2>" +
    '<p class="reason">Retention runs when you press the button; it is not scheduled. Delete previews never drop data until confirmed.</p>' +
    '<form id="retention-form" class="filters">' +
    '<label>Sample days <input id="retention-sample" type="number" min="0" value="' +
    number(retention.sample_days) +
    '" /></label>' +
    '<label>Normalized days <input id="retention-normalized" type="number" min="0" value="' +
    number(retention.normalized_days) +
    '" /></label>' +
    '<label>Audit days <input id="retention-audit" type="number" min="0" value="' +
    number(retention.audit_days) +
    '" /></label>' +
    '<button type="submit">Save retention</button>' +
    '<button type="button" id="retention-apply" class="danger">Apply retention now</button>' +
    "</form>" +
    "<h2>Loopback HTTP</h2>" +
    "<p>Status: " +
    (http.enabled ? "enabled" : "disabled") +
    " · token " +
    (http.token_set ? "set" : "missing") +
    " · host " +
    escapeText(http.host || "127.0.0.1") +
    "</p>" +
    '<p><label>Token <input id="http-token" type="password" placeholder="at least 32 characters" /></label> ' +
    '<button type="button" id="http-enable">Enable HTTP</button> ' +
    '<button type="button" id="http-disable">Disable HTTP</button></p>' +
    "<h2>Names</h2><p>Pipe " +
    (pipe.enabled ? "enabled" : "disabled") +
    " as " +
    escapeText(pipe.name) +
    "</p>" +
    "<h2>Deletion</h2>" +
    '<p class="reason">Every deletion previews counts first; the second call records an audit row. Nothing is removed by the first click.</p>' +
    '<form id="deletion-form" class="filters">' +
    '<label>Project <input id="deletion-project" placeholder="project key" /></label>' +
    '<button type="submit">Preview deletion</button>' +
    "</form>" +
    '<p id="deletion-summary" class="warning">' +
    escapeText(pending ? pending.summary : "") +
    "</p>" +
    '<p id="deletion-actions" style="display:' +
    (pending ? "block" : "none") +
    '">' +
    '<button type="button" id="deletion-confirm" class="danger">Confirm deletion</button> ' +
    '<button type="button" id="deletion-cancel">Cancel</button></p>' +
    '<p id="settings-status" role="status"></p>' +
    "</section>"
  );
}

async function refreshSettings(main) {
  const view = await fetchJSON("/api/settings");
  main.innerHTML = renderSettingsPage(view);
  wireSettings(main);
}

function wireSettings(main) {
  const status = main.querySelector("#settings-status");
  const say = (text, isError) => {
    status.textContent = text;
    status.className = isError ? "error" : "";
  };
  const run = async (promise, message) => {
    say("saving…");
    try {
      await promise;
      say(message);
      await refreshSettings(main);
    } catch (error) {
      say(error.message, true);
    }
  };
  const capture = main.querySelector("#capture-toggle");
  if (capture) {
    capture.addEventListener("click", () => {
      const enabled = capture.getAttribute("data-enabled") !== "1";
      run(
        postJSON("/api/settings/capture", { enabled }),
        enabled ? "capture resumed" : "capture paused",
      );
    });
  }
  const pauseButton = main.querySelector("#project-pause-apply");
  if (pauseButton) {
    pauseButton.addEventListener("click", () => {
      const project = main.querySelector("#project-pause").value.trim();
      run(
        postJSON("/api/settings/projects", { project, enabled: false }),
        "project paused",
      );
    });
  }
  for (const button of main.querySelectorAll("[data-connector]")) {
    button.addEventListener("click", () => {
      const enabled = button.getAttribute("data-enabled") !== "1";
      run(
        postJSON("/api/settings/connectors", {
          name: button.getAttribute("data-connector"),
          enabled,
        }),
        enabled ? "connector enabled" : "connector disabled",
      );
    });
  }
  const retention = main.querySelector("#retention-form");
  if (retention) {
    retention.addEventListener("submit", (event) => {
      event.preventDefault();
      const days = (id) => Number(main.querySelector(id).value);
      run(
        postJSON("/api/settings/retention", {
          sample_days: days("#retention-sample"),
          normalized_days: days("#retention-normalized"),
          audit_days: days("#retention-audit"),
        }),
        "retention periods saved",
      );
    });
  }
  const apply = main.querySelector("#retention-apply");
  if (apply) {
    apply.addEventListener("click", () => {
      if (
        !window.confirm(
          "Run one retention pass now? Rows older than the configured periods are removed.",
        )
      )
        return;
      run(
        postJSON("/api/settings/retention/apply", { confirmed: true }),
        "retention pass ran",
      );
    });
  }
  const httpEnable = main.querySelector("#http-enable");
  if (httpEnable) {
    httpEnable.addEventListener("click", () => {
      run(
        postJSON("/api/settings/http", {
          enabled: true,
          token: main.querySelector("#http-token").value,
        }),
        "HTTP listener enabled",
      );
    });
  }
  const httpDisable = main.querySelector("#http-disable");
  if (httpDisable) {
    httpDisable.addEventListener("click", () =>
      run(
        postJSON("/api/settings/http", { enabled: false }),
        "HTTP listener disabled",
      ),
    );
  }
  const deletion = main.querySelector("#deletion-form");
  if (deletion) {
    deletion.addEventListener("submit", async (event) => {
      event.preventDefault();
      say("previewing…");
      try {
        const counts = await postJSON("/api/deletions/preview", {
          project_key: main.querySelector("#deletion-project").value.trim(),
        });
        main.querySelector("#deletion-summary").textContent = counts.summary;
        main.querySelector("#deletion-actions").style.display = "block";
        say("preview ready; nothing has been deleted yet");
      } catch (error) {
        say(error.message, true);
      }
    });
  }
  const confirm = main.querySelector("#deletion-confirm");
  if (confirm) {
    confirm.addEventListener("click", () => {
      if (
        !window.confirm(
          "Delete what the preview counted? This recorded audit row cannot be reverted.",
        )
      )
        return;
      run(postJSON("/api/deletions/confirm", {}), "deletion done");
    });
  }
  const cancel = main.querySelector("#deletion-cancel");
  if (cancel) {
    cancel.addEventListener("click", () =>
      run(postJSON("/api/deletions/cancel", {}), "deletion cancelled"),
    );
  }
}

async function loadSettingsPage() {
  const main = mainElement();
  main.innerHTML = '<p id="status" role="status">loading…</p>';
  try {
    await refreshSettings(main);
  } catch (error) {
    main.innerHTML = '<p class="error">' + escapeText(error.message) + "</p>";
  }
}

// ---- entry point ---------------------------------------------------------
const route = location.pathname.replace(/\/$/, "") || "/";
if (route === "/stats") {
  loadStatsPage();
} else if (route === "/settings") {
  loadSettingsPage();
} else {
  loadDetail();
}
