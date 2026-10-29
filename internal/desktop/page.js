(() => {
  const invoke = (message) => window._wails.invoke(message);
  const open = (target) => invoke("desktop:open:" + new URL(target, location.href).href);
  window.open = (target) => {
    if (target) open(target);
    return null;
  };
  const route = () => invoke("desktop:route:" + location.pathname + location.search);
  for (const name of ["pushState", "replaceState"]) {
    const original = history[name];
    history[name] = function (...args) {
      const result = original.apply(this, args);
      route();
      return result;
    };
  }
  window.addEventListener("popstate", route);
  route();
  document.addEventListener("click", (event) => {
    const link = event.target.closest("a[href]");
    if (!link || event.defaultPrevented) return;
    if (link.target === "_blank" || link.origin !== location.origin) {
      event.preventDefault();
      open(link.href);
    }
  });
  window.addEventListener("dragover", (event) => {
    if (event.defaultPrevented) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "none";
  });
  window.addEventListener("drop", (event) => event.preventDefault());

  const resizable = window._wails.environment.OS !== "darwin";
  let maximised = false;
  let pending = "";
  window.addEventListener("desktop:maximised", (event) => {
    maximised = event.detail;
  });
  const edgeAt = (event) => {
    if (!resizable || maximised) return "";
    const border = 5;
    const corner = 15;
    const { clientX: x, clientY: y } = event;
    const { innerWidth: width, innerHeight: height } = window;
    let vertical = y < border ? "n" : y >= height - border ? "s" : "";
    let horizontal = x < border ? "w" : x >= width - border ? "e" : "";
    if (vertical && !horizontal) horizontal = x < corner ? "w" : x >= width - corner ? "e" : "";
    if (horizontal && !vertical) vertical = y < corner ? "n" : y >= height - corner ? "s" : "";
    return vertical || horizontal ? vertical + horizontal + "-resize" : "";
  };
  const draggable = (target) =>
    getComputedStyle(target).getPropertyValue("--wails-draggable").trim() === "drag";
  window.addEventListener(
    "mousedown",
    (event) => {
      if (event.button !== 0) return;
      const edge = edgeAt(event);
      pending = edge ? "wails:resize:" + edge : draggable(event.target) ? "wails:drag" : "";
    },
    true,
  );
  window.addEventListener(
    "mousemove",
    (event) => {
      if (pending && event.buttons === 1) {
        invoke(pending);
        pending = "";
        return;
      }
      document.documentElement.style.cursor = edgeAt(event);
    },
    true,
  );
  window.addEventListener("mouseup", () => {
    pending = "";
  }, true);
  document.addEventListener("dblclick", (event) => {
    if (draggable(event.target)) invoke("desktop:window:toggle-maximise");
  });
})();
