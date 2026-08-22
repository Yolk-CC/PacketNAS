/* PocketNAS M13 设置页 —— 服务器名展示 / 修改密码（后端接口未提供，禁用态）/ 主题切换。
 * 共享路径与人脸识别卡片已分别迁入 shares.html 与 library.html?tab=faces。 */
(function () {
  'use strict';

  var TOKEN_KEY = 'pocketnas_token';
  var $ = function (id) { return document.getElementById(id); };
  var toast = window.PocketToast || function () {};

  function api(url) {
    return fetch(url, {
      headers: { 'X-Auth-Token': localStorage.getItem(TOKEN_KEY) || '' }
    }).then(function (res) {
      if (res.status === 401) {
        location.href = 'overview.html';
        throw new Error('unauthorized');
      }
      return res.json();
    });
  }

  /* ---------- 服务器信息 ---------- */
  api('/api/system/info').then(function (info) {
    $('server-name-text').textContent = info.serverName || '未命名';
    $('sv-version').textContent = 'v' + (info.version || '?');
    $('sv-root').textContent = info.root || '–';
  }).catch(function () {
    $('server-name-text').textContent = '加载失败';
  });

  /* ---------- 密码眼睛按钮（禁用态下保留逻辑，接口上线即启用） ---------- */
  document.querySelectorAll('.pwd-eye').forEach(function (btn) {
    btn.addEventListener('click', function () {
      var input = $(btn.getAttribute('data-eye'));
      input.type = input.type === 'password' ? 'text' : 'password';
    });
  });

  /* ---------- 主题 segmented ---------- */
  var seg = $('theme-seg');
  function markTheme() {
    var cur = window.PocketTheme.get();
    seg.querySelectorAll('.tab').forEach(function (t) {
      var active = t.getAttribute('data-theme-opt') === cur;
      t.classList.toggle('active', active);
      t.setAttribute('aria-checked', active ? 'true' : 'false');
    });
  }
  seg.querySelectorAll('.tab').forEach(function (t) {
    t.addEventListener('click', function () {
      window.PocketTheme.set(t.getAttribute('data-theme-opt'));
      markTheme();
    });
  });
  markTheme();
})();
