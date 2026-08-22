/* PocketNAS M13 图库容器 —— ?tab=photos/people/faces 子页签切换，
 * 状态写入 URL query（刷新可恢复）；子页脚本按需懒加载。 */
(function () {
  'use strict';

  var $ = function (id) { return document.getElementById(id); };
  var TABS = { photos: 'gallery.js', people: 'people.js', faces: 'faces.js' };
  var loaded = {};

  function currentTab() {
    var t = new URLSearchParams(location.search).get('tab');
    return TABS[t] ? t : 'photos';
  }

  function loadScript(src) {
    if (loaded[src]) return;
    loaded[src] = true;
    var s = document.createElement('script');
    s.src = src;
    document.body.appendChild(s);
  }

  function show(tab) {
    Object.keys(TABS).forEach(function (t) {
      $('tab-' + t).classList.toggle('hidden', t !== tab);
    });
    document.querySelectorAll('#lib-tabs .tab').forEach(function (a) {
      var active = a.getAttribute('data-tab') === tab;
      a.classList.toggle('active', active);
      if (active) a.setAttribute('aria-current', 'page');
      else a.removeAttribute('aria-current');
    });
    // 页头操作按子页显隐
    $('type-filter').classList.toggle('hidden', tab !== 'photos');
    $('btn-rescan').classList.toggle('hidden', tab !== 'people');
    loadScript(TABS[tab]);
  }

  // 拦截页签点击：不整页刷新，仅切 section + 写 URL
  document.querySelectorAll('#lib-tabs .tab').forEach(function (a) {
    a.addEventListener('click', function (e) {
      e.preventDefault();
      var tab = a.getAttribute('data-tab');
      var type = new URLSearchParams(location.search).get('type');
      var q = '?tab=' + tab + (tab === 'photos' && type ? '&type=' + type : '');
      history.pushState(null, '', 'library.html' + q);
      show(tab);
    });
  });

  window.addEventListener('popstate', function () { show(currentTab()); });

  show(currentTab());
})();
