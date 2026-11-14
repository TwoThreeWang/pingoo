/* Pingoo 页面交互：主题、提示条、菜单、抽屉、章节导航、问答输入框等。大部分数据交互由 htmx 完成。 */
(function () {
  'use strict';
  var KEY = 'pingoo-theme', theme = 'auto';
  try { theme = localStorage.getItem(KEY) || 'auto'; } catch (e) {}
  function applyTheme() {
    if (theme === 'auto') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.dataset.theme = theme;
    document.querySelectorAll('[data-theme-v]').forEach(function (b) { b.setAttribute('aria-pressed', b.dataset.themeV === theme); });
    if (window.PingooCharts) window.PingooCharts.drawAll();
  }
  applyTheme();

  var tm;
  window.toast = function (msg) {
    var t = document.getElementById('toast');
    if (!t) { t = document.createElement('div'); t.id = 'toast'; t.className = 'toast'; t.setAttribute('role', 'status'); document.body.appendChild(t); }
    t.textContent = msg; t.classList.add('show'); clearTimeout(tm); tm = setTimeout(function () { t.classList.remove('show'); }, 2600);
  };
  // 服务器通过 HX-Trigger: {"toast": "..."} 发提示
  document.addEventListener('toast', function (e) { if (e.detail && e.detail.value) toast(e.detail.value); });
  // 响应同时要求跳转或刷新时，提示会随旧页面消失：先存起来，新页面加载后再显示
  document.addEventListener('htmx:beforeOnLoad', function (e) {
    var x = e.detail.xhr; if (!x || !(x.getResponseHeader('HX-Redirect') || x.getResponseHeader('HX-Refresh') === 'true')) return;
    try { var o = JSON.parse(x.getResponseHeader('HX-Trigger') || '{}'); if (o.toast) sessionStorage.setItem('pingoo-toast', o.toast); } catch (err) {}
  });
  try { var pending = sessionStorage.getItem('pingoo-toast'); if (pending) { sessionStorage.removeItem('pingoo-toast'); setTimeout(function () { toast(pending); }, 50); } } catch (err) {}
  document.addEventListener('htmx:responseError', function (e) {
    var x = e.detail.xhr;
    if (/"toast"/.test(x.getResponseHeader('HX-Trigger') || '')) return; // 服务器已经说明了原因（比如演示账号只能浏览），别用笼统的话盖掉
    toast(x.status === 403 ? '请求被拒绝，请刷新页面再试' : '出错了：' + (x.responseText || x.status).toString().slice(0, 120));
  });
  document.addEventListener('htmx:sendError', function () { toast('网络连接失败'); });

  // ---------- 请求进行中的反馈 ----------
  // 点了按钮到结果出来之前，按钮转圈、不能重复点。不然慢一点的操作（生成、同步、导入网站、测试连接、生成演示数据）看起来像没反应。
  function busyEls(elt) {
    if (!elt || !elt.matches) return [];
    if (elt.tagName === 'FORM') return [].slice.call(elt.querySelectorAll('button[type=submit],button:not([type]),input[type=submit]'));
    return elt.matches('button, a.btn, a.act') ? [elt] : []; // 轮询、懒加载这类由 div 发起的请求不用标
  }
  function setBusy(els, on) {
    els.forEach(function (b) {
      b.classList.toggle('is-busy', on);
      if (on) b.setAttribute('aria-busy', 'true'); else b.removeAttribute('aria-busy');
    });
  }
  document.addEventListener('htmx:beforeRequest', function (e) {
    var els = busyEls(e.detail.elt);
    if (els.length) { e.detail.elt._busy = els; setBusy(els, true); }
  });
  document.addEventListener('htmx:afterRequest', function (e) {
    var elt = e.detail.elt;
    if (elt && elt._busy) { setBusy(elt._busy, false); elt._busy = null; }
  });
  // 普通表单（生成卡密、注册登录等）：确认框取消、人机校验还没算完时提交会被拦下，那时不标
  document.addEventListener('submit', function (e) {
    var f = e.target;
    if (!f || f.tagName !== 'FORM' || f.hasAttribute('hx-post') || (f.getAttribute('method') || '').toLowerCase() !== 'post') return;
    setTimeout(function () { // 等其他监听（确认框、人机校验）都跑完，才知道这次提交有没有被拦下
      if (e.defaultPrevented) return;
      var els = busyEls(f);
      setBusy(els, true);
      setTimeout(function () { setBusy(els, false); }, 8000); // 下载文件（导出卡密）时页面不会跳走，过一会儿恢复
    }, 0);
  });
  addEventListener('pageshow', function (e) { // 浏览器“后退”回来时页面是缓存的，按钮还停在转圈状态
    if (e.persisted) setBusy([].slice.call(document.querySelectorAll('.is-busy')), false);
  });

  // ---------- 抽屉 ----------
  // 打开抽屉、弹窗时锁住后面的页面，不让滚轮带着它滚；补上滚动条的宽度，页面不会左右跳一下
  function lockScroll(on) {
    var de = document.documentElement;
    if (on) {
      if (de.classList.contains('lock-scroll')) return;
      de.style.setProperty('--sbw', (window.innerWidth - de.clientWidth) + 'px');
      de.classList.add('lock-scroll');
    } else {
      de.classList.remove('lock-scroll'); de.style.removeProperty('--sbw');
    }
  }
  window.openDrawer = function () {
    var d = document.getElementById('drawer'); if (!d) return;
    lockScroll(true);
    d.classList.add('on'); document.getElementById('scrim').classList.add('on');
    var c = d.querySelector('.close'); if (c) c.focus({ preventScroll: true });
  };
  // 诊断还没出结果就关掉窗口（或者离开页面、又点了另一个诊断）：结果没有地方再看了，让后台停下来，
  // 不然它会接着跑完、照常扣积分。同时清空抽屉，否则藏起来的抽屉还会每 1.5 秒去查一次进度。
  // 用 sendBeacon：页面正在关闭时普通请求可能发不出去。
  function stopDiagnosis() {
    var b = document.querySelector('#drawer-body [data-running]');
    if (!b) return;
    var url = '/diagnoses/' + b.dataset.running + '/stop';
    if (!(navigator.sendBeacon && navigator.sendBeacon(url))) fetch(url, { method: 'POST', keepalive: true });
    document.getElementById('drawer-body').innerHTML = '';
  }
  window.addEventListener('pagehide', stopDiagnosis);
  window.closeDrawer = function () {
    stopDiagnosis();
    ['drawer', 'scrim', 'modal'].forEach(function (id) { var e = document.getElementById(id); if (e) e.classList.remove('on'); });
    lockScroll(false);
  };
  window.openModal = function () {
    var m = document.getElementById('modal'); if (!m) return;
    lockScroll(true);
    m.classList.add('on'); document.getElementById('scrim').classList.add('on');
  };

  // 诊断进行中每 1.5 秒整块刷新抽屉，滚动区域被换掉、会回到顶部。刷新前记下位置，刷新后：
  // 原来停在底部（在看最新进度）的跟到最新一步；往上翻着看的保持原位；结论刚出来时滚到结论开头。
  var drawerScroll = null;
  document.addEventListener('htmx:beforeSwap', function (e) {
    if (!e.detail.target || e.detail.target.id !== 'drawer-body') return;
    var b = e.detail.target.querySelector('.dr-b');
    drawerScroll = b ? { top: b.scrollTop, atEnd: b.scrollHeight - b.scrollTop - b.clientHeight < 48, done: !!b.querySelector('.concl') } : null;
  });
  document.addEventListener('htmx:afterSwap', function (e) {
    if (!e.detail.target || e.detail.target.id !== 'drawer-body') return;
    var b = e.detail.target.querySelector('.dr-b'), s = drawerScroll;
    drawerScroll = null;
    if (!b) return;
    var concl = b.querySelector('.concl');
    if (concl) {
      if (s && s.done) b.scrollTop = s.top; // 已经出结论了（比如点了保存），不动
      else if (s && s.atEnd) b.scrollTop += concl.getBoundingClientRect().top - b.getBoundingClientRect().top - 12;
      else if (s) b.scrollTop = s.top;
      // 直接打开一份已经完成的诊断：从头看
    } else if (!s || s.atEnd) {
      b.scrollTop = b.scrollHeight;
    } else {
      b.scrollTop = s.top;
    }
  });

  document.addEventListener('click', function (e) {
    var t = e.target;
    var th = t.closest('[data-theme-v]');
    if (th) { theme = th.dataset.themeV; try { localStorage.setItem(KEY, theme); } catch (err) {} applyTheme(); return; }
    if (t.closest('[data-close]') || t.id === 'scrim') { closeDrawer(); return; }
    // 这几个以前是写在标签上的 onclick，挪到这里之后页面里没有内联脚本，CSP 可以禁止内联脚本
    var op = t.closest('[data-open]');
    if (op) { if (op.dataset.open === 'modal') openModal(); else openDrawer(); }
    if (t.closest('[data-print]')) { window.print(); return; }
    var pwb = t.closest('[data-toggle-pw]');
    if (pwb) {
      var pwi = document.getElementById(pwb.dataset.togglePw), hidden = pwi.type === 'password';
      pwi.type = hidden ? 'text' : 'password'; pwb.textContent = hidden ? '隐藏' : '显示'; pwi.focus();
      return;
    }

    // 图上的异常点、洞察里的“深入分析” → 打开诊断抽屉
    var dg = t.closest('[data-diagnose]');
    if (dg && dg.dataset.diagnose) {
      e.preventDefault();
      var u = new URL(dg.dataset.diagnose, location.href);
      var body = document.getElementById('drawer-body');
      if (!body) return;
      stopDiagnosis();
      body.innerHTML = '<div class="loading-block"><div class="skel" style="width:60%"></div><div class="skel" style="width:85%"></div><div class="skel" style="width:40%"></div></div>';
      openDrawer();
      var params = {}; u.searchParams.forEach(function (v, k) { params[k] = v; });
      htmx.ajax('POST', u.pathname, { target: '#drawer-body', values: params, swap: 'innerHTML' });
      return;
    }

    // 洞察里的“查看数据”：滚到对应区块
    var go = t.closest('[data-goto]');
    if (go) {
      e.preventDefault();
      var sec = document.getElementById(go.dataset.goto);
      if (sec) sec.scrollIntoView({ behavior: 'smooth' });
      return;
    }

    // 下拉菜单
    var mb = t.closest('[data-menu]');
    document.querySelectorAll('.menu.open, .gen-pop.open').forEach(function (m) { if (!mb || m.id !== mb.dataset.menu) m.classList.remove('open'); });
    if (mb) { var m = document.getElementById(mb.dataset.menu); if (m) { m.classList.toggle('open'); mb.setAttribute('aria-expanded', m.classList.contains('open')); } return; }

    // 概览：展开全部“需要关注”
    var ex = t.closest('[data-expand]');
    if (ex) { var box = document.getElementById(ex.dataset.expand); box.classList.toggle('expanded'); ex.textContent = box.classList.contains('expanded') ? '收起' : ex.dataset.label; return; }

    // 复制
    var cp = t.closest('[data-copy]');
    if (cp) { try { navigator.clipboard.writeText(cp.dataset.copy); toast('已复制'); } catch (err) {} return; }

    // 表格行整体可点击
    var lk = t.closest('tr[data-href], .panel[data-href], .rep[data-href]');
    if (lk && !t.closest('a,button,input,select,textarea')) { location.href = lk.dataset.href; return; }

    // 详情页：切换核心指标
    var tile = t.closest('[data-tile]');
    if (tile) { document.querySelectorAll('[data-tile]').forEach(function (b) { b.setAttribute('aria-pressed', b === tile); }); }

    // 内容表：展开 / 收起一行
    var tr = t.closest('tr[data-detail]');
    if (tr && !t.closest('a,button')) { toggleRow(tr); return; }

    // 问答：手机上打开对话列表
    if (t.closest('#menuBtn')) { document.getElementById('side').classList.toggle('open'); return; }
    // 问答：推荐问题和追问建议
    var ask = t.closest('[data-ask]');
    if (ask) {
      var ta = document.getElementById('ta');
      if (ta) { ta.value = ask.dataset.ask; ta.dispatchEvent(new Event('input')); document.getElementById('chatForm').requestSubmit(); }
      return;
    }
  });
  function toggleRow(tr) {
    var det = tr.nextElementSibling;
    if (!det || !det.classList.contains('detail')) return;
    var slot = det.querySelector('.detail-slot');
    if (tr.classList.contains('open')) {
      tr.classList.remove('open'); tr.setAttribute('aria-expanded', 'false'); det.hidden = true; slot.innerHTML = '';
      return;
    }
    tr.classList.add('open'); tr.setAttribute('aria-expanded', 'true'); det.hidden = false;
    slot.innerHTML = '<div class="loading-block"><div class="skel" style="width:70%"></div><div class="skel" style="width:40%"></div></div>';
    htmx.ajax('GET', tr.dataset.detail, { target: slot, swap: 'innerHTML' });
  }

  // 下拉框一选就提交（排序、筛选）
  document.addEventListener('change', function (e) {
    var el = e.target.closest && e.target.closest('[data-autosubmit]');
    if (el && el.form) el.form.submit();
  });
  // 提交前要确认的表单（删除对话）
  document.addEventListener('submit', function (e) {
    var f = e.target.closest && e.target.closest('form[data-confirm]');
    if (f && !confirm(f.dataset.confirm)) e.preventDefault();
  });

  document.addEventListener('keydown', function (e) {
    if (e.key === 'Enter' && e.target.matches('tr[data-detail]')) { toggleRow(e.target); return; }
    if (e.key === 'Escape') { closeDrawer(); document.querySelectorAll('.menu.open,.gen-pop.open').forEach(function (m) { m.classList.remove('open'); }); }
    if (e.key === 'Enter' && e.target.matches('tr[data-href], .rep[data-href]')) location.href = e.target.dataset.href;
  });

  // ---------- 详情页：章节导航高亮 ----------
  function initSecnav() {
    var links = [].slice.call(document.querySelectorAll('.secnav a[href^="#"]'));
    if (!links.length || !('IntersectionObserver' in window)) return;
    var io = new IntersectionObserver(function (es) {
      es.forEach(function (en) {
        if (en.isIntersecting) links.forEach(function (a) { a.classList.toggle('on', a.getAttribute('href') === '#' + en.target.id); });
      });
    }, { rootMargin: '-120px 0px -65% 0px' });
    document.querySelectorAll('section.blk').forEach(function (s) { io.observe(s); });
  }

  // ---------- 设置页：子导航 ----------
  function route() {
    var panels = document.querySelectorAll('.spanel');
    if (!panels.length) return;
    var first = panels[0].id.slice(2); // 没有锚点或锚点不对时显示第一节（设置页是“连接”，后台是“概况”）
    var id = (location.hash || '#' + first).slice(1);
    if (!document.getElementById('p-' + id)) id = first;
    panels.forEach(function (p) { p.classList.toggle('on', p.id === 'p-' + id); });
    document.querySelectorAll('.subnav a').forEach(function (a) { a.classList.toggle('on', a.getAttribute('href') === '#' + id); });
  }
  addEventListener('hashchange', route);

  // ---------- 问答 ----------
  function initChat() {
    var ta = document.getElementById('ta'); if (!ta) return;
    var send = document.getElementById('send');
    var stream = document.getElementById('stream');
    var sync = function () { ta.style.height = 'auto'; ta.style.height = Math.min(ta.scrollHeight, 180) + 'px'; send.disabled = !ta.value.trim(); };
    ta.addEventListener('input', sync);
    ta.addEventListener('keydown', function (e) {
      if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) { e.preventDefault(); if (ta.value.trim()) document.getElementById('chatForm').requestSubmit(); }
    });
    document.getElementById('chatForm').addEventListener('htmx:beforeRequest', function () { send.disabled = true; });
    document.getElementById('chatForm').addEventListener('htmx:afterRequest', function () { ta.value = ''; sync(); });
    if (stream) stream.scrollTop = stream.scrollHeight;
    sync();
  }
  document.addEventListener('htmx:afterSwap', function (e) {
    var stream = document.getElementById('stream');
    if (stream && stream.contains(e.target)) stream.scrollTop = stream.scrollHeight;
  });

  // ---------- 主题面板 ----------
  function initProto() {
    if (document.querySelector('.proto') || document.body.dataset.noTheme) return;
    var p = document.createElement('div'); p.className = 'proto no-print';
    p.innerHTML = '主题 <button data-theme-v="auto">自动</button><button data-theme-v="light">浅</button><button data-theme-v="dark">深</button>';
    document.body.appendChild(p);
  }

  // 设置页：从“编辑”链接（/settings?edit=3#sites）过来时，直接打开那个网站的编辑抽屉
  function openEditFromURL() {
    var m = location.search.match(/[?&]edit=(\d+)/); if (!m) return;
    var b = document.querySelector('[data-edit="' + m[1] + '"]'); if (b) b.click();
  }

  document.addEventListener('DOMContentLoaded', function () { initProto(); applyTheme(); initSecnav(); route(); initChat(); openEditFromURL(); });
})();
