const $ = (id) => document.getElementById(id);
const els = {
  form: $("open-form"), url: $("url"), docs: $("docs"), status: $("status"),
  frame: $("frame"), empty: $("viewer-empty"), messages: $("messages"),
  chatForm: $("chat-form"), text: $("text"), send: $("send"),
  quoteBox: $("quote-box"), quote: $("quote"), quoteClear: $("quote-clear"),
  askSel: $("ask-selection"),
  top: $("top"), bar: $("bar"), barTitle: $("bar-title"), barStatus: $("bar-status"),
  controls: $("controls"), collapse: $("collapse"),
};

let doc = null;          // currently open Doc
let quote = "";          // pending quote to attach to the next message
let selection = "";      // latest selection reported by the iframe
let events = null;       // EventSource for the current doc
const seen = new Set();  // message ids already rendered

async function api(method, path, body) {
  const res = await fetch(path, {
    method, headers: body ? { "Content-Type": "application/json" } : {},
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

// --- documents ---------------------------------------------------------

async function refreshDocs() {
  const docs = await api("GET", "/api/docs");
  els.docs.innerHTML = '<option value="">Previously opened…</option>';
  for (const d of docs.slice().reverse()) {
    const o = document.createElement("option");
    o.value = d.id; o.textContent = d.title || d.url;
    if (doc && d.id === doc.id) o.selected = true;
    els.docs.appendChild(o);
  }
}

async function openDoc(d) {
  doc = d;
  history.replaceState(null, "", "#" + d.id);
  document.title = (d.title || d.url) + " · Reader";
  els.url.value = d.url;
  els.empty.hidden = true;
  els.frame.setAttribute("sandbox", "allow-same-origin allow-scripts allow-popups");
  els.frame.hidden = false;
  // PDFs open in the vendored PDF.js viewer so text can be selected and quoted.
  els.frame.src = d.kind === "pdf" ? `/docs/${d.id}/view` : `/docs/${d.id}/raw`;
  els.text.disabled = false; els.send.disabled = false;
  els.barTitle.textContent = d.title || d.url;
  els.collapse.hidden = false;
  setCollapsed(true);
  setQuote("");
  seen.clear();
  els.messages.innerHTML = "";
  for (const m of await api("GET", `/api/messages?docId=${d.id}`)) render(m);
  subscribe();
  refreshDocs();
}

els.form.addEventListener("submit", async (e) => {
  e.preventDefault();
  const btn = els.form.querySelector("button");
  btn.disabled = true; btn.textContent = "Opening…";
  try {
    await openDoc(await api("POST", "/api/open", { url: els.url.value.trim() }));
  } catch (err) {
    alert("Could not open: " + err.message);
  } finally {
    btn.disabled = false; btn.textContent = "Open";
  }
});

els.docs.addEventListener("change", async () => {
  if (!els.docs.value) return;
  const docs = await api("GET", "/api/docs");
  const d = docs.find((x) => x.id === els.docs.value);
  if (d) openDoc(d);
  else refreshDocs(); // evicted by another tab; drop it from the list
});

// --- chat ---------------------------------------------------------------

function render(m) {
  if (seen.has(m.id)) return;
  seen.add(m.id);
  const div = document.createElement("div");
  div.className = "msg " + m.role;
  if (m.quote) {
    const q = document.createElement("blockquote");
    q.textContent = m.quote;
    div.appendChild(q);
  }
  const p = document.createElement("div");
  p.className = "body";
  p.textContent = m.text;
  div.appendChild(p);
  const t = document.createElement("time");
  t.textContent = new Date(m.time).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  div.appendChild(t);
  els.messages.appendChild(div);
  els.messages.scrollTop = els.messages.scrollHeight;
  updatePending();
}

function updatePending() {
  const msgs = [...els.messages.querySelectorAll(".msg")];
  const last = msgs[msgs.length - 1];
  let hint = $("pending");
  if (last && last.classList.contains("user")) {
    if (!hint) {
      hint = document.createElement("div");
      hint.id = "pending"; hint.className = "pending";
      hint.textContent = "Waiting for a reply…";
    }
    els.messages.appendChild(hint);
  } else if (hint) hint.remove();
}

function subscribe() {
  if (events) events.close();
  events = new EventSource(`/api/events?docId=${doc.id}`);
  events.addEventListener("message", (e) => render(JSON.parse(e.data)));
}

async function send() {
  const text = els.text.value.trim();
  if (!text || !doc) return;
  els.send.disabled = true;
  try {
    const m = await api("POST", "/api/messages", { docId: doc.id, text, quote });
    render(m);
    els.text.value = "";
    autosize();
    setQuote("");
  } catch (err) {
    alert("Send failed: " + err.message);
  } finally {
    els.send.disabled = false;
    els.text.focus();
  }
}

// Grow the textarea with its content, up to the max-height set in CSS.
function autosize() {
  els.text.style.height = "auto";
  els.text.style.height = els.text.scrollHeight + "px";
}
els.text.addEventListener("input", autosize);

els.chatForm.addEventListener("submit", (e) => { e.preventDefault(); send(); });
els.text.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); }
});

// --- quoting ------------------------------------------------------------

function setQuote(q) {
  quote = q.trim();
  els.quote.textContent = quote;
  els.quoteBox.hidden = !quote;
}

window.addEventListener("message", (e) => {
  if (e.data && e.data.type === "reader-selection") {
    selection = e.data.text || "";
    els.askSel.disabled = !selection.trim();
  }
});

els.askSel.addEventListener("click", () => {
  if (selection.trim()) { setQuote(selection); els.text.focus(); }
});
els.quoteClear.addEventListener("click", () => setQuote(""));

// --- header collapse ----------------------------------------------------

function setCollapsed(on) {
  if (!doc) on = false;
  els.controls.hidden = on;
  els.bar.hidden = !on;
  if (!on && doc) els.url.select();
}
els.bar.addEventListener("click", () => setCollapsed(false));
els.collapse.addEventListener("click", () => setCollapsed(true));
document.addEventListener("keydown", (e) => {
  const typing = /^(INPUT|TEXTAREA)$/.test(document.activeElement?.tagName);
  if (e.key === "/" && !typing) { e.preventDefault(); setCollapsed(false); }
  else if (e.key === "Escape" && doc && !els.controls.hidden) { setCollapsed(true); els.url.blur(); }
});

// --- status -------------------------------------------------------------

function setStatus(text, cls) {
  for (const el of [els.status, els.barStatus]) { el.textContent = text; el.className = "status " + cls; }
}
async function pollStatus() {
  try {
    const s = await api("GET", "/api/status");
    const n = s.agents || 0;
    setStatus(n > 1 ? `● ${n} agents attached` : n === 1 ? "● agent attached" : "○ no agent attached", n > 0 ? "on" : "off");
  } catch {
    setStatus("○ server unreachable", "off");
  }
}
setInterval(pollStatus, 5000);

// --- boot ---------------------------------------------------------------

(async () => {
  pollStatus();
  await refreshDocs();
  const id = location.hash.slice(1);
  if (id) {
    const docs = await api("GET", "/api/docs");
    const d = docs.find((x) => x.id === id);
    if (d) openDoc(d);
    else history.replaceState(null, "", location.pathname); // evicted; stay on the empty state
  }
})();
