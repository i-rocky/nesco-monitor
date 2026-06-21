(function () {
  "use strict";
  var reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  /* ---------------- the meter readout ---------------- */
  var lcd = document.querySelector(".lcd");
  var valEl = document.getElementById("lcdValue");
  var statusEl = document.getElementById("meterStatus");
  var ledEl = document.getElementById("meterLed");
  var flashEl = document.getElementById("meterFlash");

  function fmt(n) { return n.toFixed(2); }
  function setStatus(txt, cls) {
    statusEl.textContent = txt;
    statusEl.className = "status " + (cls || "");
    ledEl.className = "led " + (cls || "");
  }

  if (lcd && valEl) {
    var bal = 1284.5;
    valEl.textContent = fmt(bal);
    if (reduce) {
      setStatus("LIVE", "");
    } else {
      var state = "live";
      setStatus("LIVE", "");
      setInterval(function () {
        if (state === "recharge") return;
        bal -= 0.7 + Math.abs(Math.sin(Date.now() / 600)) * 0.7; // ~0.7–1.4 / tick
        if (bal <= 60) {
          state = "recharge";
          lcd.classList.remove("is-low");
          lcd.classList.add("is-charge");
          setStatus("RECHARGED", "charge");
          flashEl.textContent = "RECHARGE  ↑ ৳2,400";
          flashEl.classList.add("show");
          bal += 2400;
          setTimeout(function () {
            flashEl.classList.remove("show");
            lcd.classList.remove("is-charge");
            setStatus("LIVE", "");
            state = "live";
          }, 1200);
        } else if (bal < 200 && state === "live") {
          state = "low";
          lcd.classList.add("is-low");
          setStatus("LOW BALANCE", "low");
        }
        valEl.textContent = fmt(bal);
      }, 110);
    }
  }

  /* ---------------- terminal ---------------- */
  var cmdEl = document.getElementById("termCmd");
  var cursorEl = document.getElementById("termCursor");
  var logEl = document.getElementById("termLog");
  var cmd = "docker run -d --env-file .env -v nesco-data:/data wpkpda/nesco-monitor";

  function streamLog() {
    if (!logEl) return;
    var lines = Array.prototype.slice.call(logEl.querySelectorAll(".ln"));
    if (reduce) {
      lines.forEach(function (l) { l.style.opacity = 1; });
      return;
    }
    var i = 0;
    (function next() {
      if (i >= lines.length) {
        var end = document.createElement("div");
        end.className = "ln";
        end.innerHTML = '<span class="prompt">$</span> <span class="cursor"></span>';
        logEl.appendChild(end);
        return;
      }
      lines[i].style.opacity = 1;
      i++;
      setTimeout(next, 420);
    })();
  }

  if (cmdEl) {
    if (reduce) {
      cmdEl.textContent = cmd;
      if (cursorEl) cursorEl.remove();
      streamLog();
    } else {
      var ci = 0;
      (function type() {
        if (ci <= cmd.length) {
          cmdEl.textContent = cmd.slice(0, ci);
          ci++;
          setTimeout(type, 26);
        } else {
          if (cursorEl) cursorEl.remove();
          setTimeout(streamLog, 350);
        }
      })();
    }
  }

  /* ---------------- scroll reveals ---------------- */
  var revealEls = document.querySelectorAll(".reveal");
  if ("IntersectionObserver" in window && !reduce) {
    var io = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (e.isIntersecting) { e.target.classList.add("in"); io.unobserve(e.target); }
      });
    }, { threshold: 0.16 });
    revealEls.forEach(function (el) { io.observe(el); });
  } else {
    revealEls.forEach(function (el) { el.classList.add("in"); });
  }

  /* ---------------- copy buttons ---------------- */
  document.querySelectorAll(".copy").forEach(function (btn) {
    btn.addEventListener("click", function () {
      var pre = btn.parentElement.querySelector("pre");
      if (!pre) return;
      navigator.clipboard.writeText(pre.innerText.trim()).then(function () {
        var old = btn.textContent;
        btn.textContent = "copied";
        setTimeout(function () { btn.textContent = old; }, 1400);
      });
    });
  });
})();
