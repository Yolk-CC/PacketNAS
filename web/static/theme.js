/* PocketNAS M13 — theme.js：主题读写 + 导航选中态 + 导航主题/退出按钮。
 * 所有页面引用。主题优先级：localStorage 'pocket-theme' (light/dark/auto) > 系统偏好。 */
(function () {
  'use strict';

  var THEME_KEY = 'pocket-theme';
  var TOKEN_KEY = 'pocketnas_token';
  var mql = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;

  /* ---------- 主题 ---------- */
  function getTheme() {
    return localStorage.getItem(THEME_KEY) || 'auto';
  }

  function applyTheme(mode) {
    var root = document.documentElement;
    if (mode === 'light' || mode === 'dark') {
      root.dataset.theme = mode;
    } else {
      // auto：移除属性交由 prefers-color-scheme 媒体查询接管
      root.removeAttribute('data-theme');
    }
    updateThemeButtons();
  }

  function setTheme(mode) {
    localStorage.setItem(THEME_KEY, mode);
    applyTheme(mode);
  }

  function effectiveDark() {
    var m = getTheme();
    if (m === 'dark') return true;
    if (m === 'light') return false;
    return !!(mql && mql.matches);
  }

  function toggleTheme() {
    setTheme(effectiveDark() ? 'light' : 'dark');
  }

  function updateThemeButtons() {
    var dark = effectiveDark();
    document.querySelectorAll('[data-action="toggle-theme"]').forEach(function (btn) {
      btn.innerHTML = dark ? ICONS.sun : ICONS.moon;
      btn.setAttribute('aria-label', dark ? '切换为浅色模式' : '切换为深色模式');
      btn.title = dark ? '浅色模式' : '深色模式';
    });
  }

  if (mql && mql.addEventListener) {
    mql.addEventListener('change', function () {
      if (getTheme() === 'auto') updateThemeButtons();
    });
  }

  /* ---------- 内联 SVG 图标（24 viewBox，stroke currentColor） ---------- */
  var ICONS = {
    sun: '<svg class="icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></svg>',
    moon: '<svg class="icon" viewBox="0 0 24 24"><path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/></svg>',
    logout: '<svg class="icon" viewBox="0 0 24 24"><path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/><path d="m16 17 5-5-5-5M21 12H9"/></svg>'
  };

  /* ---------- 导航选中态 ---------- */
  function markActive() {
    var file = (location.pathname.split('/').pop() || 'overview.html');
    if (file === '' || file === 'index.html') file = 'overview.html';
    var query = location.search;
    document.querySelectorAll('.nav-item, .m-tab').forEach(function (a) {
      var href = a.getAttribute('href') || '';
      var hFile = href.split('?')[0];
      var active = hFile === file && (hFile !== 'library.html' || href.indexOf('?') === -1 || href.slice(href.indexOf('?')) === query);
      // library 一级项在三子页均高亮
      if (file === 'library.html' && hFile === 'library.html' && href.indexOf('?') === -1) active = true;
      if (active) {
        a.classList.add('active');
        a.setAttribute('aria-current', 'page');
      }
    });
  }

  /* ---------- 退出登录 ---------- */
  function bindLogout() {
    document.querySelectorAll('[data-action="logout"]').forEach(function (btn) {
      btn.addEventListener('click', function () {
        localStorage.removeItem(TOKEN_KEY);
        location.href = 'overview.html';
      });
    });
  }

  function bindThemeButtons() {
    document.querySelectorAll('[data-action="toggle-theme"]').forEach(function (btn) {
      btn.addEventListener('click', toggleTheme);
    });
    updateThemeButtons();
  }

  /* ---------- 服务器名同步到 brand 与标题 ---------- */
  function syncServerName() {
    fetch('/api/system/info', {
      headers: { 'X-Auth-Token': localStorage.getItem(TOKEN_KEY) || '' }
    }).then(function (res) {
      if (!res.ok) return null;
      return res.json();
    }).then(function (info) {
      if (!info || !info.serverName) return;
      document.querySelectorAll('.brand').forEach(function (b) { b.textContent = info.serverName; });
      var t = document.title;
      if (t.indexOf('PocketNAS') === 0) document.title = info.serverName + t.slice('PocketNAS'.length);
    }).catch(function () {});
  }

  document.addEventListener('DOMContentLoaded', function () {
    markActive();
    bindLogout();
    bindThemeButtons();
    syncServerName();
  });

  /* ---------- Toast（顶部居中堆叠，最多 3 条） ---------- */
  var TOAST_ICONS = {
    success: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="m8.5 12.5 2.5 2.5 5-5.5"/></svg>',
    error: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="m9 9 6 6M15 9l-6 6"/></svg>',
    info: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 8h.01M12 11v5"/></svg>'
  };

  function toast(msg, type) {
    type = type || 'info';
    var stack = document.getElementById('toast-stack');
    if (!stack) {
      stack = document.createElement('div');
      stack.id = 'toast-stack';
      document.body.appendChild(stack);
    }
    while (stack.children.length >= 3) stack.removeChild(stack.firstChild);
    var el = document.createElement('div');
    el.className = 'toast ' + type;
    el.innerHTML = (TOAST_ICONS[type] || TOAST_ICONS.info) + '<span></span>';
    el.querySelector('span').textContent = msg;
    stack.appendChild(el);
    var ttl = type === 'error' ? 5000 : 2500;
    var timer = setTimeout(dismiss, ttl);
    function dismiss() {
      clearTimeout(timer);
      if (el.parentNode) el.parentNode.removeChild(el);
    }
    if (type === 'error') {
      var x = document.createElement('button');
      x.className = 'toast-x';
      x.textContent = '×';
      x.setAttribute('aria-label', '关闭');
      x.addEventListener('click', dismiss);
      el.appendChild(x);
    }
  }

  /* 导出给设置页等使用 */
  window.PocketTheme = {
    get: getTheme,
    set: setTheme,
    isDark: effectiveDark,
    THEME_KEY: THEME_KEY
  };
  window.PocketToast = toast;
})();
