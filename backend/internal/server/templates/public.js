// Progressive enhancement for public pages. Everything here is optional: without JavaScript the
// pages still work (plain links and a multipart form).
(function () {
  "use strict";

  // Relative times, corrected for the difference between this device's clock and the server's.
  var serverNow = Number(document.body.getAttribute("data-now")) || Date.now();
  var skew = serverNow - Date.now();
  var rtf = window.Intl && Intl.RelativeTimeFormat ? new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }) : null;
  document.querySelectorAll("time[data-rel]").forEach(function (el) {
    var t = Number(el.getAttribute("data-rel"));
    var d = new Date(t);
    el.title = d.toLocaleString();
    el.dateTime = d.toISOString();
    if (!rtf) { el.textContent = d.toLocaleString(); return; }
    var diff = (t - (Date.now() + skew)) / 1000;
    var units = [["year", 31536000], ["month", 2592000], ["week", 604800], ["day", 86400], ["hour", 3600], ["minute", 60]];
    for (var i = 0; i < units.length; i++) {
      if (Math.abs(diff) >= units[i][1]) { el.textContent = rtf.format(Math.round(diff / units[i][1]), units[i][0]); return; }
    }
    el.textContent = rtf.format(Math.round(diff), "second");
  });

  var form = document.getElementById("upload-form");
  if (!form || !window.XMLHttpRequest || !window.TextEncoder) return;

  var endpoint = form.getAttribute("data-tus");
  var maxBytes = Number(form.getAttribute("data-max")) || 0;
  var types = (form.getAttribute("data-types") || "").split(",").filter(Boolean);
  var input = form.querySelector('input[type=file]');
  var queue = document.getElementById("queue");
  var drop = document.getElementById("drop");
  var CHUNK = 16 * 1024 * 1024;

  function b64(s) {
    var bytes = new TextEncoder().encode(s), bin = "";
    for (var i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
    return btoa(bin);
  }
  function size(n) {
    var u = ["B", "KB", "MB", "GB", "TB"], i = 0;
    while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
    return (i ? n.toFixed(1) : n) + " " + u[i];
  }
  function errMsg(xhr) {
    try { return JSON.parse(xhr.responseText).error.message; } catch (e) { return "Upload failed (" + xhr.status + ")."; }
  }
  function req(method, url, headers, body, onProgress) {
    return new Promise(function (resolve) {
      var x = new XMLHttpRequest();
      x.open(method, url);
      Object.keys(headers).forEach(function (k) { x.setRequestHeader(k, headers[k]); });
      if (onProgress) x.upload.onprogress = function (e) { onProgress(e.loaded); };
      x.onload = function () { resolve(x); };
      x.onerror = x.ontimeout = x.onabort = function () { resolve(null); }; // network failure → retry
      x.send(body);
    });
  }
  function wait(ms) { return new Promise(function (r) { setTimeout(r, ms); }); }
  function online() {
    return navigator.onLine ? Promise.resolve() : new Promise(function (r) { window.addEventListener("online", r, { once: true }); });
  }

  function row(file) {
    var li = document.createElement("li");
    li.innerHTML = '<div class="q-top"><span class="q-name"></span><span class="q-state">Waiting…</span></div><div class="bar"><i></i></div>';
    li.querySelector(".q-name").textContent = file.name + " · " + size(file.size);
    queue.appendChild(li);
    return {
      state: function (t, cls) { var s = li.querySelector(".q-state"); s.textContent = t; s.className = "q-state " + (cls || ""); },
      progress: function (p) { li.querySelector(".bar i").style.width = (p * 100).toFixed(1) + "%"; }
    };
  }

  async function uploadOne(file, ui, uploader) {
    if (maxBytes && file.size > maxBytes) { ui.state("Too large (max " + size(maxBytes) + ")", "err"); return false; }
    if (types.length) {
      var ext = ("." + file.name.split(".").pop()).toLowerCase();
      if (file.name.indexOf(".") < 0 || types.indexOf(ext) < 0) { ui.state("File type not accepted", "err"); return false; }
    }
    var key = "ferry-up:" + endpoint + ":" + [file.name, file.size, file.lastModified].join("|");
    var url = null, offset = 0, attempt = 0;
    try { url = localStorage.getItem(key); } catch (e) {}
    var tus = { "Tus-Resumable": "1.0.0" };
    while (true) {
      await online();
      if (url) {
        var h = await req("HEAD", url, tus, null);
        if (h && h.status === 200) {
          offset = Number(h.getResponseHeader("Upload-Offset")) || 0;
        } else if (h && (h.status === 404 || h.status === 410 || h.status === 403)) {
          url = null;
        } else if (!h || h.status >= 500 || h.status === 429) {
          attempt++; ui.state("Reconnecting…"); await wait(Math.min(30000, 1000 * Math.pow(2, attempt))); continue;
        }
      }
      if (!url) {
        var meta = "filename " + b64(file.name) + (uploader ? ",uploader " + b64(uploader) : "");
        var c = await req("POST", endpoint, Object.assign({ "Upload-Length": String(file.size), "Upload-Metadata": meta }, tus), null);
        if (!c) { attempt++; ui.state("Reconnecting…"); await wait(Math.min(30000, 1000 * Math.pow(2, attempt))); continue; }
        if (c.status !== 201) { ui.state(errMsg(c), "err"); return false; }
        url = new URL(c.getResponseHeader("Location"), location.href).href;
        offset = 0;
        try { localStorage.setItem(key, url); } catch (e) {}
        if (file.size === 0) break;
      }
      if (offset >= file.size) break;
      var end = Math.min(file.size, offset + CHUNK);
      ui.state(Math.floor(offset / file.size * 100) + "%");
      var base = offset;
      var p = await req("PATCH", url, Object.assign({ "Upload-Offset": String(offset), "Content-Type": "application/offset+octet-stream" }, tus),
        file.slice(offset, end), function (loaded) { ui.progress((base + loaded) / file.size); ui.state(Math.floor((base + loaded) / file.size * 100) + "%"); });
      if (p && p.status === 204) {
        attempt = 0;
        offset = Number(p.getResponseHeader("Upload-Offset")) || end;
        if (offset >= file.size) break;
        continue;
      }
      if (p && p.status === 409) continue; // offset mismatch → HEAD again
      if (p && p.status >= 400 && p.status < 500 && p.status !== 423 && p.status !== 429) {
        ui.state(errMsg(p), "err");
        try { localStorage.removeItem(key); } catch (e) {}
        return false;
      }
      attempt++;
      ui.state("Connection lost — retrying…");
      await wait(Math.min(30000, 1000 * Math.pow(2, attempt)));
    }
    try { localStorage.removeItem(key); } catch (e) {}
    ui.progress(1);
    ui.state("Uploaded", "ok");
    return true;
  }

  // Uploads start as soon as files are picked or dropped; more can be added while others run.
  var pending = [], running = false, done = 0, failed = 0;
  var title = drop.querySelector(".drop-title");
  var idleTitle = title.textContent;
  form.classList.add("js");

  function enqueue(list) {
    var files = Array.prototype.slice.call(list || []);
    if (!files.length) return;
    files.forEach(function (f) { pending.push({ file: f, ui: row(f) }); });
    run();
  }

  async function run() {
    if (running) return;
    running = true;
    var uploader = ((form.querySelector("input[name=uploader]") || {}).value || "").trim();
    while (pending.length) {
      title.textContent = "Uploading… " + (pending.length > 1 ? pending.length + " files waiting" : "");
      var job = pending.shift();
      if (await uploadOne(job.file, job.ui, uploader)) done++; else failed++;
    }
    running = false;
    title.textContent = idleTitle;
    if (done > 0) {
      title.textContent = done + " file" + (done === 1 ? "" : "s") + " uploaded" + (failed ? " · " + failed + " failed" : "");
      setTimeout(function () { if (!running) location.reload(); }, failed ? 4000 : 1500);
    }
  }

  input.addEventListener("change", function () {
    enqueue(input.files);
    input.value = "";
  });
  // Handle drops ourselves (anywhere on the page), so files never open in the browser instead.
  ["dragenter", "dragover"].forEach(function (ev) {
    document.addEventListener(ev, function (e) {
      if (e.dataTransfer && Array.prototype.indexOf.call(e.dataTransfer.types || [], "Files") >= 0) { e.preventDefault(); drop.classList.add("over"); }
    });
  });
  document.addEventListener("dragleave", function (e) { if (!e.relatedTarget) drop.classList.remove("over"); });
  document.addEventListener("drop", function (e) {
    if (!e.dataTransfer || !e.dataTransfer.files.length) return;
    e.preventDefault();
    drop.classList.remove("over");
    enqueue(e.dataTransfer.files);
  });
  form.addEventListener("submit", function (e) {
    e.preventDefault();
    enqueue(input.files);
    input.value = "";
  });
})();
