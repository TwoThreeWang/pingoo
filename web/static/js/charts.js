/* Pingoo 图表：折线图（本期 + 上期）、十字线提示、标注、异常点。数据来自元素上的 data-chart。 */
(function () {
  'use strict';
  var NS = 'http://www.w3.org/2000/svg';
  function el(tag, attrs) { var e = document.createElementNS(NS, tag); for (var k in attrs) e.setAttribute(k, attrs[k]); return e; }
  function tip() {
    var t = document.getElementById('tt');
    if (!t) { t = document.createElement('div'); t.id = 'tt'; t.className = 'tt'; t.setAttribute('role', 'tooltip'); document.body.appendChild(t); }
    return t;
  }

  var fmtInt = function (v) { return Math.round(v).toLocaleString('en-US'); };
  function fmtKind(kind) {
    switch (kind) {
      case 'pct': return function (v) { return (v * 100).toFixed(1) + '%'; };
      case 'dur': return function (v) { v = Math.round(v); return v < 60 ? v + ' 秒' : Math.floor(v / 60) + ' 分 ' + (v % 60) + ' 秒'; };
      case 'dec': return function (v) { return v.toFixed(2); };
      case 'pos': return function (v) { return v.toFixed(1); };
    }
    return fmtInt;
  }
  function tickFmt(kind) {
    switch (kind) {
      case 'pct': return function (t) { return Math.round(t * 100) + '%'; };
      case 'dur': return function (t) { t = Math.round(t); return Math.floor(t / 60) + ':' + String(t % 60).padStart(2, '0'); };
      case 'dec': return function (t) { return t.toFixed(1); };
      case 'pos': return function (t) { return t.toFixed(0); };
    }
    return fmtInt;
  }
  function niceTicks(lo, hi, count) {
    var span = Math.max(hi - lo, 1e-9), raw = span / count, mag = Math.pow(10, Math.floor(Math.log10(raw))), n = raw / mag;
    var step = (n < 1.5 ? 1 : n < 3 ? 2 : n < 7 ? 5 : 10) * mag, start = Math.floor(lo / step) * step, out = [];
    for (var v = start; v <= hi + step * 0.5 && out.length < 10; v += step) out.push(+v.toFixed(10));
    return out;
  }
  function textW(s) { var w = 0; for (var i = 0; i < s.length; i++) w += /[　-鿿＀-￯《》“”]/.test(s[i]) ? 11.5 : 6.6; return w; }

  function tooltipAt(px, py, build) {
    var t = tip(); t.replaceChildren(); build(t); t.classList.add('on');
    var tw = t.offsetWidth, th = t.offsetHeight, left = px + 14, top = py - th - 12;
    if (left + tw > innerWidth - 8) left = px - tw - 14;
    if (left < 8) left = 8;
    if (top < 60) top = py + 18;
    t.style.left = left + 'px'; t.style.top = top + 'px';
  }
  function hideTip() { var t = document.getElementById('tt'); if (t) t.classList.remove('on'); }
  function row(box, val, lab, color) {
    var r = document.createElement('div'); r.className = 'r';
    var k = document.createElement('i'); k.style.background = color;
    var s = document.createElement('strong'); s.textContent = val;
    var l = document.createElement('span'); l.textContent = lab;
    r.append(k, s, l); box.appendChild(r);
  }

  function draw(host) {
    var o;
    try { o = JSON.parse(host.dataset.chart); } catch (e) { return; }
    host.querySelectorAll('svg,.marker').forEach(function (n) { n.remove(); });
    var W = host.clientWidth, H = host.clientHeight;
    var cur = o.cur || [], prev = o.prev || [], n = cur.length;
    if (!W || !H || !n) { return; }
    var hasPrev = prev.length === n;
    if (!hasPrev) prev = [];
    var fv = fmtKind(o.kind), labels = o.labels || { cur: [], prev: [] };
    var hasAnn = o.ann && o.ann.length;
    var padT = hasAnn ? (o.axes ? 40 : 22) : 6, padB = o.axes ? 26 : 6, padL = o.axes ? 48 : 2, padR = 8;
    var all = cur.concat(prev), lo, hi, ticks = null;
    var mn = Math.min.apply(null, all), mx = Math.max.apply(null, all);
    if (o.axes) {
      var base = o.zero ? 0 : mn - (mx - mn) * 0.25;
      ticks = niceTicks(Math.max(0, base), mx + (mx - base) * 0.05 || 1, 4); lo = ticks[0]; hi = ticks[ticks.length - 1];
    } else {
      var sp = Math.max(mx - mn, Math.abs(mx) * 0.02, 1e-9); lo = Math.max(0, mn - sp * 0.3); hi = mx + sp * 0.08;
    }
    var x = function (i) { return padL + (n === 1 ? (W - padL - padR) / 2 : i * (W - padL - padR) / (n - 1)); };
    var y = function (v) { var t = (v - lo) / Math.max(hi - lo, 1e-9); return padT + (o.invert ? t : 1 - t) * (H - padT - padB); };
    var path = function (a) { return a.map(function (v, i) { return (i ? 'L' : 'M') + x(i).toFixed(1) + ' ' + y(v).toFixed(1); }).join(''); };
    var svg = el('svg', { width: W, height: H, viewBox: '0 0 ' + W + ' ' + H, 'aria-hidden': 'true' });
    svg.style.display = 'block'; svg.style.overflow = 'visible';
    if (ticks) {
      var tf = tickFmt(o.kind);
      ticks.forEach(function (t) {
        var yy = Math.round(y(t)) + 0.5;
        svg.appendChild(el('line', { x1: padL, x2: W - padR, y1: yy, y2: yy, stroke: 'var(--line)', 'stroke-width': 1 }));
        var tx = el('text', { x: padL - 10, y: yy + 4, 'text-anchor': 'end', 'font-size': 11, fill: 'var(--ink-3)' }); tx.textContent = tf(t); svg.appendChild(tx);
      });
      var every = Math.max(1, Math.ceil(n / 6));
      for (var i = 0; i < n; i += every) {
        var tx = el('text', { x: x(i), y: H - 6, 'text-anchor': i === 0 ? 'start' : 'middle', 'font-size': 11, fill: 'var(--ink-3)' });
        tx.textContent = (labels.cur[i] || '').replace(/^(今天|昨天) /, ''); svg.appendChild(tx);
      }
    } else {
      svg.appendChild(el('line', { x1: 0, x2: W, y1: H - padB + 0.5, y2: H - padB + 0.5, stroke: 'var(--line)', 'stroke-width': 1 }));
    }
    if (!o.invert) svg.appendChild(el('path', { d: path(cur) + 'L' + x(n - 1) + ' ' + y(lo) + 'L' + x(0) + ' ' + y(lo) + 'Z', fill: 'var(--accent-wash)' }));
    if (hasPrev) svg.appendChild(el('path', { d: path(prev), fill: 'none', stroke: 'var(--prev)', 'stroke-width': 1.5, 'stroke-linejoin': 'round', 'stroke-linecap': 'round' }));
    svg.appendChild(el('path', { d: path(cur), fill: 'none', stroke: 'var(--accent)', 'stroke-width': 2, 'stroke-linejoin': 'round', 'stroke-linecap': 'round' }));
    if (o.anomaly && n > 1) svg.appendChild(el('path', { d: 'M' + x(n - 2) + ' ' + y(cur[n - 2]) + 'L' + x(n - 1) + ' ' + y(cur[n - 1]), stroke: 'var(--bad)', 'stroke-width': 2, 'stroke-linecap': 'round', fill: 'none' }));
    if (hasAnn) {
      var rows = [[], []];
      o.ann.filter(function (a) { return a.i >= 0 && a.i < n; }).sort(function (a, b) { return a.i - b.i; }).forEach(function (a) {
        var ax = x(a.i), w = textW(a.text), right = ax > W * 0.72, x0 = right ? ax - w : ax, x1 = right ? ax : ax + w;
        var r = rows[0].some(function (b) { return !(x1 + 8 < b[0] || x0 - 8 > b[1]); }) ? 1 : 0;
        if (r === 1 && rows[1].some(function (b) { return !(x1 + 8 < b[0] || x0 - 8 > b[1]); })) return;
        rows[r].push([x0, x1]);
        var ty = r === 0 ? 11 : 26;
        svg.appendChild(el('line', { x1: ax, x2: ax, y1: ty + 4, y2: Math.max(ty + 6, y(cur[a.i]) - 7), stroke: 'var(--ink-3)', 'stroke-width': 1 }));
        var t = el('text', { x: ax, y: ty, 'text-anchor': right ? 'end' : 'start', 'font-size': 11.5, fill: 'var(--ink-2)' }); t.textContent = a.text; svg.appendChild(t);
      });
    }
    svg.appendChild(el('circle', { cx: x(n - 1), cy: y(cur[n - 1]), r: 4, fill: o.anomaly ? 'var(--bad)' : 'var(--accent)', stroke: 'var(--bg)', 'stroke-width': 2 }));
    var cross = el('line', { y1: padT - 4, y2: H - padB, stroke: 'var(--ink-3)', 'stroke-width': 1, opacity: 0 });
    var dP = el('circle', { r: 4, fill: 'var(--prev)', stroke: 'var(--bg)', 'stroke-width': 2, opacity: 0 });
    var dC = el('circle', { r: 4, fill: 'var(--accent)', stroke: 'var(--bg)', 'stroke-width': 2, opacity: 0 });
    svg.append(cross, dP, dC);
    host.appendChild(svg);
    (o.markers || []).filter(function (m) { return m.i >= 0 && m.i < n; }).forEach(function (m) {
      var b = document.createElement('button'); b.className = 'marker'; b.type = 'button'; b.textContent = '!';
      b.style.left = x(m.i) + 'px'; b.style.top = y(cur[m.i]) + 'px'; b.title = m.text; b.setAttribute('aria-label', m.text + '，点击让 AI 诊断');
      b.dataset.diagnose = m.url || '';
      host.appendChild(b);
    });
    function show(i, cx, cy) {
      i = Math.max(0, Math.min(n - 1, i)); host._i = i;
      cross.setAttribute('x1', x(i)); cross.setAttribute('x2', x(i)); cross.setAttribute('opacity', 0.6);
      dC.setAttribute('cx', x(i)); dC.setAttribute('cy', y(cur[i])); dC.setAttribute('opacity', 1);
      if (hasPrev) { dP.setAttribute('cx', x(i)); dP.setAttribute('cy', y(prev[i])); dP.setAttribute('opacity', 1); }
      var r = host.getBoundingClientRect();
      tooltipAt(cx != null ? cx : r.left + x(i), cy != null ? cy : r.top + y(cur[i]), function (box) {
        var h = document.createElement('div'); h.className = 'h'; h.textContent = (o.title ? o.title : '') + (labels.cur[i] ? (o.title ? ' · ' : '') + labels.cur[i] : ''); box.appendChild(h);
        row(box, fv(cur[i]), '本期 · ' + (o.name || '访客'), 'var(--accent)');
        if (hasPrev) row(box, fv(prev[i]), (o.prevName || '上期') + ' · ' + (labels.prev[i] || ''), 'var(--prev)');
      });
    }
    function hide() { cross.setAttribute('opacity', 0); dC.setAttribute('opacity', 0); dP.setAttribute('opacity', 0); hideTip(); }
    host.onpointermove = function (e) {
      if (e.target.closest && e.target.closest('.marker')) { hide(); return; }
      var r = host.getBoundingClientRect();
      show(Math.round((e.clientX - r.left - padL) / ((W - padL - padR) / Math.max(n - 1, 1))), e.clientX, e.clientY);
    };
    host.onpointerleave = hide; host.onblur = hide;
    host.onfocus = function () { show(n - 1); };
    host.onkeydown = function (e) {
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') { e.preventDefault(); show((host._i == null ? n - 1 : host._i) + (e.key === 'ArrowLeft' ? -1 : 1)); }
    };
  }

  function drawAll(root) { (root || document).querySelectorAll('[data-chart]').forEach(draw); }
  window.PingooCharts = { drawAll: drawAll, hideTip: hideTip, tooltipAt: tooltipAt };

  document.addEventListener('DOMContentLoaded', function () { drawAll(); });
  document.addEventListener('htmx:afterSettle', function (e) { drawAll(e.target); });
  var lastW = 0, raf;
  addEventListener('resize', function () {
    if (innerWidth === lastW) return; lastW = innerWidth;
    cancelAnimationFrame(raf); raf = requestAnimationFrame(function () { drawAll(); });
  });

  // 热力图提示
  document.addEventListener('pointermove', function (e) {
    var c = e.target.closest && e.target.closest('.heat .cell');
    if (!c) return;
    var p = (c.dataset.heat || '').split('|');
    tooltipAt(e.clientX, e.clientY, function (box) {
      var h = document.createElement('div'); h.className = 'h'; h.textContent = p[0]; box.appendChild(h);
      var r = document.createElement('div'); r.textContent = '访问 ' + p[1] + ' 次（' + p[2] + '）'; box.appendChild(r);
    });
  });
  document.addEventListener('pointerout', function (e) { if (e.target.closest && e.target.closest('.heat .cell')) hideTip(); });
})();
