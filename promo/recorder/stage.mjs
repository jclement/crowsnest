// What the recorder adds to the real app's page so a viewer can follow it: a visible cursor that
// tracks real mouse events, a ripple on each click or tap, and a spotlight that dims everything
// but the detail the narration is about. None of it changes the app; it is drawn in one
// pointer-events:none layer on top. It also rewrites the demo's throwaway state paths
// (/tmp/cnpromo-…/demo-src/crowsnest) to what they stand for (~/src/crowsnest) in visible text.
//
// stageScript(options) returns the source of an init script (page.addInitScript).

export function stageScript({ root, cursor = true, folded = true }) {
  return `(${install.toString()})(${JSON.stringify({ root, cursor, folded })});`;
}

function install({ root, cursor, folded }) {
  try {
    if (folded) sessionStorage.setItem("crowsnest-tray-folded", "1");
  } catch {}
  const ACCENT = "#4ea8f5";
  const css = `
  #promo-layer { position: fixed; inset: 0; pointer-events: none; z-index: 2147483647; }
  #promo-cursor { position: absolute; left: 0; top: 0; width: 26px; height: 26px; transform: translate(-200px,-200px);
    filter: drop-shadow(0 2px 3px rgba(0,0,0,.55)); transition: opacity .2s; }
  #promo-cursor.down svg { transform: scale(.86); transform-origin: 3px 3px; }
  .promo-ripple { position: absolute; width: 44px; height: 44px; margin: -22px 0 0 -22px; border-radius: 50%;
    border: 3px solid ${ACCENT}; background: rgba(78,168,245,.25); animation: promo-ripple .6s ease-out forwards; }
  .promo-tap { position: absolute; width: 54px; height: 54px; margin: -27px 0 0 -27px; border-radius: 50%;
    background: rgba(255,255,255,.35); border: 2px solid rgba(255,255,255,.85); animation: promo-tap .7s ease-out forwards; }
  @keyframes promo-ripple { from { transform: scale(.4); opacity: 1 } to { transform: scale(1.7); opacity: 0 } }
  @keyframes promo-tap { 0% { transform: scale(.5); opacity: .95 } 100% { transform: scale(1.5); opacity: 0 } }
  .promo-spot { position: absolute; border-radius: 10px; box-shadow: 0 0 0 9999px rgba(3,7,13,.55), 0 0 0 2px ${ACCENT}, 0 0 24px 4px rgba(78,168,245,.55);
    opacity: 0; transition: opacity .35s ease, left .45s ease, top .45s ease, width .45s ease, height .45s ease; }
  .promo-spot.on { opacity: 1; }
  .promo-ring { position: absolute; border-radius: 10px; box-shadow: 0 0 0 2px ${ACCENT}, 0 0 20px 3px rgba(78,168,245,.5);
    opacity: 0; transition: opacity .35s ease; }
  .promo-ring.on { opacity: 1; }
  `;
  const arrow = `<svg width="26" height="26" viewBox="0 0 26 26"><path d="M3 2 L3 21 L8.2 16.4 L11.8 24.2 L15 22.8 L11.5 15.2 L18.5 15.2 Z"
    fill="#fff" stroke="#0b1220" stroke-width="1.6" stroke-linejoin="round"/></svg>`;

  function layer() {
    let el = document.getElementById("promo-layer");
    if (el) return el;
    if (!document.body) return null;
    const style = document.createElement("style");
    style.textContent = css;
    document.head.appendChild(style);
    el = document.createElement("div");
    el.id = "promo-layer";
    if (cursor) el.innerHTML = `<div id="promo-cursor">${arrow}</div>`;
    document.body.appendChild(el);
    return el;
  }

  let pos = { x: -200, y: -200 };
  function place() {
    const c = document.getElementById("promo-cursor");
    if (c) c.style.transform = `translate(${pos.x - 3}px, ${pos.y - 2}px)`;
  }
  addEventListener("mousemove", (e) => { pos = { x: e.clientX, y: e.clientY }; layer(); place(); }, true);
  addEventListener("mousedown", (e) => {
    const l = layer();
    document.getElementById("promo-cursor")?.classList.add("down");
    if (!l) return;
    const r = document.createElement("div");
    r.className = "promo-ripple";
    r.style.left = `${e.clientX}px`;
    r.style.top = `${e.clientY}px`;
    l.appendChild(r);
    setTimeout(() => r.remove(), 700);
  }, true);
  addEventListener("mouseup", () => document.getElementById("promo-cursor")?.classList.remove("down"), true);

  // Paths: the demo keeps its state in a throwaway directory; show the paths it stands in for.
  const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const base = `(?:/private)?${esc(root)}/tmp/crowsnest-demo-\\d+`;
  const rules = [
    [new RegExp(`${base}/state/demo-src/`, "g"), "~/src/"],
    [new RegExp(`${base}/state/`, "g"), "~/.local/state/crowsnest/"],
    [new RegExp(`${base}/?`, "g"), "~/"],
    [new RegExp(`(?:/private)?${esc(root)}/`, "g"), "~/"],
  ];
  function fix(node) {
    if (node.nodeType === 3) {
      const v = node.nodeValue;
      if (!v || !v.includes("cnpromo-")) return;
      let n = v;
      for (const [re, to] of rules) n = n.replace(re, to);
      if (n !== v) node.nodeValue = n;
      return;
    }
    if (node.nodeType !== 1 || node.id === "promo-layer") return;
    const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
    for (let t = walker.nextNode(); t; t = walker.nextNode()) fix(t);
    if (node.title && node.title.includes("cnpromo-")) for (const [re, to] of rules) node.title = node.title.replace(re, to);
  }
  new MutationObserver((records) => {
    for (const r of records) {
      if (r.type === "characterData") fix(r.target);
      else r.addedNodes.forEach(fix);
    }
  }).observe(document, { subtree: true, childList: true, characterData: true });
  document.addEventListener("DOMContentLoaded", () => fix(document.body));

  window.__promo = {
    spot(rect, { pad = 8, dim = true } = {}) {
      const l = layer();
      let s = l.querySelector(dim ? ".promo-spot" : ".promo-ring");
      const fresh = !s;
      if (fresh) {
        s = document.createElement("div");
        s.className = dim ? "promo-spot" : "promo-ring";
        l.appendChild(s);
      }
      Object.assign(s.style, { left: `${rect.x - pad}px`, top: `${rect.y - pad}px`,
        width: `${rect.width + pad * 2}px`, height: `${rect.height + pad * 2}px` });
      if (fresh) requestAnimationFrame(() => requestAnimationFrame(() => s.classList.add("on")));
      else s.classList.add("on");
    },
    unspot() {
      document.querySelectorAll(".promo-spot, .promo-ring").forEach((s) => {
        s.classList.remove("on");
        setTimeout(() => s.remove(), 400);
      });
    },
    tap(x, y) {
      const l = layer();
      const r = document.createElement("div");
      r.className = "promo-tap";
      r.style.left = `${x}px`;
      r.style.top = `${y}px`;
      l.appendChild(r);
      setTimeout(() => r.remove(), 800);
    },
    hideCursor(h) {
      const c = document.getElementById("promo-cursor");
      if (c) c.style.opacity = h ? "0" : "1";
    },
  };
}
