// 人机校验（工作量证明）：找一个 nonce，使 SHA-256(salt + ":" + nonce) 的前 bits 位都是 0。
// 同一个文件三种用法：页面里控制表单；作为 Web Worker 在后台计算；在 Node 里跑测试（module.exports）。
// 不用 crypto.subtle：它每次哈希都是一次异步调用，算一百万次太慢，而且只在 HTTPS 下可用。
(function () {
  'use strict';

  var K = new Int32Array([
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
  ]);
  var IV = new Int32Array([0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19]);

  // 对一个 64 字节的块做一次压缩（消息不超过 55 字节，一个块就够）。out 收到 8 个 32 位字。
  var W = new Int32Array(64);
  function compress(block, out) {
    var i, t1, t2, x, y;
    for (i = 0; i < 16; i++) {
      W[i] = (block[i * 4] << 24) | (block[i * 4 + 1] << 16) | (block[i * 4 + 2] << 8) | block[i * 4 + 3];
    }
    for (i = 16; i < 64; i++) {
      x = W[i - 15]; y = W[i - 2];
      W[i] = (((x >>> 7 | x << 25) ^ (x >>> 18 | x << 14) ^ (x >>> 3)) + W[i - 16] +
              ((y >>> 17 | y << 15) ^ (y >>> 19 | y << 13) ^ (y >>> 10)) + W[i - 7]) | 0;
    }
    var a = IV[0], b = IV[1], c = IV[2], d = IV[3], e = IV[4], f = IV[5], g = IV[6], h = IV[7];
    for (i = 0; i < 64; i++) {
      t1 = (h + ((e >>> 6 | e << 26) ^ (e >>> 11 | e << 21) ^ (e >>> 25 | e << 7)) + ((e & f) ^ (~e & g)) + K[i] + W[i]) | 0;
      t2 = (((a >>> 2 | a << 30) ^ (a >>> 13 | a << 19) ^ (a >>> 22 | a << 10)) + ((a & b) ^ (a & c) ^ (b & c))) | 0;
      h = g; g = f; f = e; e = (d + t1) | 0; d = c; c = b; b = a; a = (t1 + t2) | 0;
    }
    out[0] = (IV[0] + a) | 0; out[1] = (IV[1] + b) | 0; out[2] = (IV[2] + c) | 0; out[3] = (IV[3] + d) | 0;
    out[4] = (IV[4] + e) | 0; out[5] = (IV[5] + f) | 0; out[6] = (IV[6] + g) | 0; out[7] = (IV[7] + h) | 0;
  }

  // 把 ASCII 字符串填成一个已经补位的块。
  function fill(block, s) {
    var n = s.length, i;
    for (i = 0; i < n; i++) block[i] = s.charCodeAt(i);
    block[n] = 0x80;
    for (i = n + 1; i < 62; i++) block[i] = 0;
    block[62] = (n * 8) >>> 8; block[63] = (n * 8) & 0xff;
  }

  function leadingZeroBits(words) {
    var n = 0;
    for (var i = 0; i < 8; i++) {
      var z = Math.clz32(words[i]);
      n += z;
      if (z < 32) break;
    }
    return n;
  }

  // sha256 只支持不超过 55 个字符的 ASCII 字符串（够用了），返回 8 个 32 位字。
  function sha256(s) {
    var block = new Uint8Array(64), out = new Int32Array(8);
    fill(block, s);
    compress(block, out);
    return out;
  }

  function hex(words) {
    var s = '';
    for (var i = 0; i < 8; i++) s += ('00000000' + (words[i] >>> 0).toString(16)).slice(-8);
    return s;
  }

  // search 从 start 开始逐个试 nonce，最多试 count 个；找到返回 nonce 字符串，没找到返回 null。
  function search(salt, bits, start, count) {
    var prefix = salt + ':', block = new Uint8Array(64), out = new Int32Array(8);
    for (var n = start, end = start + count; n < end; n++) {
      fill(block, prefix + n);
      compress(block, out);
      if (leadingZeroBits(out) >= bits) return String(n);
    }
    return null;
  }

  var api = { sha256: sha256, hex: hex, leadingZeroBits: leadingZeroBits, search: search };

  // ---------- Node（测试） ----------
  if (typeof module === 'object' && module.exports) { module.exports = api; return; }

  // ---------- Web Worker ----------
  if (typeof document === 'undefined') {
    self.onmessage = function (e) {
      var d = e.data;
      if (d.bench) { // 测速：算 d.bench 次，回报耗时
        var t = Date.now(); search(d.salt, 256, 0, d.bench); self.postMessage({ ms: Date.now() - t }); return;
      }
      for (var n = d.start; ; n += 50000) {
        var r = search(d.salt, d.bits, n, 50000);
        if (r !== null) { self.postMessage({ nonce: r }); return; }
      }
    };
    return;
  }

  // ---------- 页面 ----------
  var script = document.currentScript;
  var workerURL = script ? script.src : '/static/js/pow.js';
  var MIN_WAIT = 2100; // 页面打开不足 2.1 秒不提交（服务端把 2 秒内提交的当作脚本）

  // solve 用几个 Worker 并行找 nonce（各自从不同的起点数），不支持 Worker 时在主线程分批算，不卡页面。
  function solve(salt, bits, done) {
    var finished = false;
    function finish(n) { if (!finished) { finished = true; done(n); } }
    var workers = [];
    try {
      var k = Math.min(Math.max(navigator.hardwareConcurrency || 2, 1), 4);
      for (var i = 0; i < k; i++) {
        var w = new Worker(workerURL);
        w.onmessage = function (e) { workers.forEach(function (x) { x.terminate(); }); finish(e.data.nonce); };
        w.onerror = function () { workers.forEach(function (x) { x.terminate(); }); if (!finished) mainThread(); };
        w.postMessage({ salt: salt, bits: bits, start: i * 1e12 });
        workers.push(w);
      }
      return;
    } catch (err) {
      workers.forEach(function (x) { x.terminate(); });
    }
    mainThread();
    function mainThread() {
      var n = 0;
      (function step() {
        if (finished) return;
        var r = search(salt, bits, n, 20000);
        n += 20000;
        if (r !== null) finish(r); else setTimeout(step, 0);
      })();
    }
  }

  function now() { return window.performance && performance.now ? performance.now() : Date.now(); }

  function init(form) {
    var tokenEl = form.querySelector('input[name="pow_token"]'), nonceEl = form.querySelector('input[name="pow_nonce"]');
    if (!tokenEl || !nonceEl) return;
    var parts = tokenEl.value.split('.');
    if (parts.length !== 5) return;
    var opened = now(), nonce = null, waiting = false;
    var btn = form.querySelector('button[type="submit"], button:not([type])');
    var ready = function () { return nonce !== null && now() - opened >= MIN_WAIT; };
    var go = function () {
      if (!waiting) return;
      if (ready()) { waiting = false; HTMLFormElement.prototype.submit.call(form); return; }
      if (nonce !== null) setTimeout(go, Math.max(MIN_WAIT - (now() - opened), 0) + 20);
    };
    solve(parts[3], +parts[2], function (n) { nonce = n; nonceEl.value = n; go(); });
    form.addEventListener('submit', function (e) {
      if (ready()) return;
      e.preventDefault();
      waiting = true;
      if (btn) { btn.disabled = true; btn.textContent = '正在进行安全校验…'; }
      go();
    });
  }

  function start() { Array.prototype.forEach.call(document.querySelectorAll('form[data-pow]'), init); }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start); else start();

  // 测速：在控制台运行 PingooPow.bench() 看这台设备每秒能算多少次
  window.PingooPow = {
    bench: function (n, cb) {
      n = n || 300000;
      var w = new Worker(workerURL);
      w.onmessage = function (e) { var rate = Math.round(n / e.data.ms * 1000); w.terminate(); (cb || console.log)(rate); };
      w.postMessage({ bench: n, salt: '0123456789abcdef0123456789abcdef' });
    },
    search: search
  };
})();
