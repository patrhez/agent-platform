const state = { pods: [], selectedPod: null, logTimer: null, events: [] };
const elements = {
  pods: document.querySelector("#pods"),
  summary: document.querySelector("#summary"),
  error: document.querySelector("#error"),
  logs: document.querySelector("#logs"),
  logTitle: document.querySelector("#log-title"),
  autoRefresh: document.querySelector("#auto-refresh"),
  timeline: document.querySelector("#timeline"),
  clusterDot: document.querySelector("#cluster-dot"),
  clusterLabel: document.querySelector("#cluster-label"),
  observedAt: document.querySelector("#observed-at"),
};

document.querySelector("#refresh").addEventListener("click", refreshPods);
elements.autoRefresh.addEventListener("change", scheduleLogs);

async function request(path, options = {}) {
  const response = await fetch(path, options);
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload.error || `Request failed (${response.status})`);
  return payload;
}

async function refreshPods() {
  try {
    const payload = await request("/api/pods");
    state.pods = payload.pods;
    elements.observedAt.textContent = new Date(payload.observedAt).toLocaleTimeString();
    setClusterState("healthy", `${state.pods.filter((pod) => pod.ready).length}/${state.pods.length} Ready`);
    hideError();
    renderSummary();
    renderPods();
    if (state.selectedPod && !state.pods.some((pod) => pod.name === state.selectedPod)) {
      record(`Pod ${state.selectedPod} disappeared; waiting for a replacement Pod to become Ready.`);
      state.selectedPod = null;
      elements.logTitle.textContent = "Select a Pod to view logs";
    }
  } catch (error) {
    setClusterState("error", "Cluster unavailable");
    showError(error.message);
  }
}

function renderSummary() {
  const ready = state.pods.filter((pod) => pod.ready).length;
  const api = state.pods.filter((pod) => pod.component === "api" && pod.ready).length;
  const worker = state.pods.filter((pod) => pod.component === "worker" && pod.ready).length;
  const restarts = state.pods.reduce((total, pod) => total + pod.restarts, 0);
  elements.summary.innerHTML = [
    ["Total Ready", `${ready} / ${state.pods.length}`],
    ["API Replicas", api],
    ["Worker Replicas", worker],
    ["Container Restarts", restarts],
  ].map(([label, value]) => `<div class="metric"><span>${label}</span><strong>${value}</strong></div>`).join("");
}

function renderPods() {
  elements.pods.replaceChildren(...state.pods.map((pod) => {
    const article = document.createElement("article");
    const healthClass = pod.ready ? "ready" : pod.phase === "Failed" ? "failed" : "pending";
    article.className = `pod ${healthClass}${pod.name === state.selectedPod ? " selected" : ""}`;
    const status = pod.ready ? "● Ready" : `○ ${escapeHTML(pod.phase)}`;
    const terminateButton = pod.terminable
      ? '<button class="danger terminate">Terminate</button>'
      : "";
    article.innerHTML = `
      <div class="role">${escapeHTML(pod.component)}</div>
      <div><div class="pod-name">${escapeHTML(pod.name)}</div>
      <div class="pod-meta">
        <span>${status}</span><span>${escapeHTML(pod.node || "Unscheduled")}</span>
        <span>${formatAge(pod.ageSeconds)}</span><span>Restarts ${pod.restarts}</span>
      </div></div>
      <div class="pod-actions">
        <button class="secondary logs">Logs</button>${terminateButton}
      </div>`;
    article.querySelector(".logs").addEventListener("click", () => selectPod(pod.name));
    article.querySelector(".terminate")?.addEventListener(
      "click",
      (event) => terminatePod(pod.name, event.currentTarget),
    );
    return article;
  }));
}

async function selectPod(podName) {
  state.selectedPod = podName;
  elements.logTitle.textContent = podName;
  renderPods();
  await refreshLogs();
  scheduleLogs();
}

async function refreshLogs() {
  if (!state.selectedPod) return;
  try {
    const payload = await request(`/api/pods/${encodeURIComponent(state.selectedPod)}/logs?tail=300`);
    elements.logs.textContent = payload.logs || "Pod has no logs yet.";
    elements.logs.scrollTop = elements.logs.scrollHeight;
  } catch (error) {
    elements.logs.textContent = `Failed to read logs: ${error.message}`;
  }
}

async function terminatePod(podName, button) {
  if (!window.confirm(`Force-terminate ${podName}? The Deployment will create a replacement Pod.`)) return;
  button.disabled = true;
  try {
    await request(`/api/pods/${encodeURIComponent(podName)}/terminate`, {
      method: "POST",
      headers: { "X-HA-Dashboard-Action": "terminate" },
    });
    record(`Injected fault on ${podName}: force-terminated Pod.`);
    await refreshPods();
  } catch (error) {
    showError(error.message);
  } finally {
    button.disabled = false;
  }
}

function scheduleLogs() {
  window.clearInterval(state.logTimer);
  if (elements.autoRefresh.checked) state.logTimer = window.setInterval(refreshLogs, 2000);
}

function record(message) {
  state.events.unshift({ time: new Date(), message });
  state.events = state.events.slice(0, 30);
  elements.timeline.replaceChildren(...state.events.map((entry) => {
    const item = document.createElement("li");
    const time = document.createElement("time");
    const text = document.createElement("span");
    time.textContent = entry.time.toLocaleTimeString();
    text.textContent = entry.message;
    item.append(time, text);
    return item;
  }));
}

function setClusterState(kind, label) {
  elements.clusterDot.className = `dot ${kind === "healthy" ? "" : kind}`;
  elements.clusterLabel.textContent = label;
}
function showError(message) { elements.error.textContent = message; elements.error.classList.remove("hidden"); }
function hideError() { elements.error.classList.add("hidden"); }
function formatAge(seconds) {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h`;
}
function escapeHTML(value) {
  const node = document.createElement("span");
  node.textContent = value || "—";
  return node.innerHTML;
}

refreshPods();
window.setInterval(refreshPods, 2000);
scheduleLogs();
