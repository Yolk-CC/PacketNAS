/* PocketNAS M13 共享页 —— 自旧设置页共享路径部分迁入。
 * 保存即生效：每次增删后立即 PUT /api/settings/shares（后端仅提供整表保存接口）。
 * 目录选择器沿用旧 dir-modal 逻辑，样式套用新 token。 */
(function () {
  'use strict';

  var TOKEN_KEY = 'pocketnas_token';
  var state = {
    shares: [],     // [{name, path}]
    browsePath: ''  // 目录选择器当前路径（'' 表示系统根）
  };

  /* ---------- DOM ---------- */
  var $ = function (id) { return document.getElementById(id); };
  var shareListEl = $('share-list'), emptyHint = $('empty-hint');
  var legacyHint = $('legacy-hint');
  var toast = window.PocketToast || function () {};

  /* ---------- 工具 ---------- */
  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }

  function api(url, options) {
    options = options || {};
    options.headers = options.headers || {};
    options.headers['X-Auth-Token'] = getToken();
    return fetch(url, options).then(function (res) {
      if (res.status === 401) {
        window.location.href = 'overview.html';
        throw new Error('unauthorized');
      }
      var ct = res.headers.get('Content-Type') || '';
      var bodyPromise = ct.indexOf('application/json') !== -1 ? res.json() : res.text();
      return bodyPromise.then(function (body) {
        if (!res.ok) {
          var msg = (body && body.error && body.error.message) || ('请求失败 (' + res.status + ')');
          toast(msg, 'error');
          throw new Error(msg);
        }
        return body;
      });
    });
  }

  /* ---------- 保存（增删后立即生效） ---------- */
  function persist(successMsg) {
    return api('/api/settings/shares', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ shares: state.shares })
    }).then(function (body) {
      state.shares = (body && body.shares) || state.shares;
      legacyHint.classList.toggle('hidden', !(body && body.legacy));
      renderShares();
      if (successMsg) toast(successMsg, 'success');
    }).catch(function () {
      loadShares(); // 失败回滚到服务端状态
    });
  }

  /* ---------- 共享列表 ---------- */
  function loadShares() {
    api('/api/settings/shares').then(function (body) {
      state.shares = (body && body.shares) || [];
      legacyHint.classList.toggle('hidden', !body || !body.legacy);
      renderShares();
    }).catch(function () {});
  }

  function renderShares() {
    shareListEl.innerHTML = '';
    emptyHint.classList.toggle('hidden', state.shares.length > 0);
    state.shares.forEach(function (share, i) {
      var tr = document.createElement('tr');

      var nameTd = document.createElement('td');
      nameTd.textContent = share.name;
      var pathTd = document.createElement('td');
      pathTd.className = 'path-cell';
      pathTd.textContent = share.path;

      var opsTd = document.createElement('td');
      opsTd.className = 'ops-col';
      var renBtn = document.createElement('button');
      renBtn.className = 'op-btn';
      renBtn.textContent = '重命名';
      renBtn.addEventListener('click', function () { renameShare(i); });
      var delBtn = document.createElement('button');
      delBtn.className = 'op-btn danger';
      delBtn.textContent = '删除';
      delBtn.addEventListener('click', function () { confirmDelete(i); });
      opsTd.appendChild(renBtn);
      opsTd.appendChild(delBtn);

      tr.appendChild(nameTd); tr.appendChild(pathTd); tr.appendChild(opsTd);
      shareListEl.appendChild(tr);
    });
  }

  function renameShare(i) {
    var share = state.shares[i];
    var nameTd = shareListEl.children[i].children[0];
    var input = document.createElement('input');
    input.className = 'input rename-input';
    input.value = share.name;
    nameTd.textContent = '';
    nameTd.appendChild(input);
    input.focus();
    input.select();
    var done = false;
    function cancel() { if (!done) { done = true; renderShares(); } }
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') { cancel(); return; }
      if (e.key !== 'Enter') return;
      var v = input.value.trim();
      if (!v || v === share.name) { cancel(); return; }
      if (v.indexOf('/') !== -1 || v === '.' || v === '..') {
        toast('名称不能包含 /', 'error'); cancel(); return;
      }
      for (var j = 0; j < state.shares.length; j++) {
        if (j !== i && state.shares[j].name === v) {
          toast('名称已存在', 'error'); cancel(); return;
        }
      }
      done = true;
      state.shares[i] = { name: v, path: share.path };
      persist('已重命名为「' + v + '」');
    });
    input.addEventListener('blur', cancel);
  }

  /* ---------- 删除确认 ---------- */
  var delIndex = -1;
  function confirmDelete(i) {
    delIndex = i;
    $('del-desc').textContent = '确定移除共享「' + state.shares[i].name +
      '」吗？只移除访问入口，不会删除磁盘上的文件';
    $('del-modal').classList.remove('hidden');
    $('del-ok').focus();
  }
  $('del-cancel').addEventListener('click', function () {
    $('del-modal').classList.add('hidden');
  });
  $('del-ok').addEventListener('click', function () {
    var name = state.shares[delIndex] ? state.shares[delIndex].name : '';
    $('del-modal').classList.add('hidden');
    if (delIndex < 0) return;
    state.shares.splice(delIndex, 1);
    delIndex = -1;
    persist('已移除共享「' + name + '」');
  });

  /* ---------- 添加共享模态 ---------- */
  function updateAddBtn() {
    $('btn-add').disabled = !$('share-name').value.trim() || !$('share-path').value.trim();
  }

  function openAddModal() {
    $('share-name').value = '';
    $('share-path').value = '';
    updateAddBtn();
    $('add-modal').classList.remove('hidden');
    $('share-name').focus();
  }
  function closeAddModal() { $('add-modal').classList.add('hidden'); }

  $('btn-open-add').addEventListener('click', openAddModal);
  $('btn-empty-add').addEventListener('click', openAddModal);
  $('fab-add').addEventListener('click', openAddModal);
  $('add-cancel').addEventListener('click', closeAddModal);
  $('share-name').addEventListener('input', updateAddBtn);

  $('btn-add').addEventListener('click', function () {
    var name = $('share-name').value.trim();
    var path = $('share-path').value.trim();
    if (!name || !path) { updateAddBtn(); return; }
    if (name.indexOf('/') !== -1 || name === '.' || name === '..') {
      toast('名称不能包含 /', 'error'); return;
    }
    for (var i = 0; i < state.shares.length; i++) {
      if (state.shares[i].name === name) { toast('名称已存在', 'error'); return; }
    }
    var btn = this;
    btn.classList.add('loading');
    btn.disabled = true;
    state.shares.push({ name: name, path: path });
    persist('已添加共享「' + name + '」').then(function () {
      btn.classList.remove('loading');
      closeAddModal();
      updateAddBtn();
    });
  });

  /* ---------- 目录选择器 ---------- */
  var modal = $('dir-modal'), dirListEl = $('dir-list');
  var dirEmpty = $('dir-empty'), browsePathEl = $('browse-path');

  function openModal() {
    state.browsePath = '';
    modal.classList.remove('hidden');
    loadDir('');
  }
  function closeModal() { modal.classList.add('hidden'); }

  function loadDir(path) {
    var url = '/api/system/browse';
    if (path) url += '?path=' + encodeURIComponent(path);
    api(url).then(function (body) {
      var entries = (body && body.dirs) || [];
      // 服务端可能重定向起始路径（如 Android 默认落到共享存储根），
      // 以响应里的实际 path 为准
      state.browsePath = (body && body.path) || path;
      browsePathEl.textContent = state.browsePath || '/';
      renderDirs(entries);
    }).catch(function () {});
  }

  function renderDirs(entries) {
    dirListEl.innerHTML = '';
    dirEmpty.classList.toggle('hidden', entries.length > 0);
    entries.forEach(function (entry) {
      var li = document.createElement('li');
      li.className = 'dir-item';
      li.innerHTML = '<svg class="icon" viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>' +
        '<span>' + escapeHtml(entry.name || entry.Name) + '</span>';
      li.addEventListener('click', function () { loadDir(entry.path || entry.Path); });
      dirListEl.appendChild(li);
    });
  }

  $('btn-browse').addEventListener('click', openModal);
  $('btn-cancel').addEventListener('click', closeModal);
  $('btn-up').addEventListener('click', function () {
    var p = state.browsePath;
    if (!p) return;
    var idx = p.replace(/[\\/]+$/, '').lastIndexOf('/');
    if (idx === -1) idx = p.lastIndexOf('\\');
    var parent = idx > 0 ? p.substring(0, idx) : '';
    loadDir(parent);
  });
  $('btn-choose').addEventListener('click', function () {
    if (!state.browsePath) { toast('请先进入一个目录', 'error'); return; }
    $('share-path').value = state.browsePath;
    updateAddBtn();
    closeModal();
  });

  // Esc 关闭模态
  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    ['add-modal', 'del-modal', 'dir-modal'].forEach(function (id) {
      $(id).classList.add('hidden');
    });
  });

  /* ---------- 启动 ---------- */
  loadShares();
})();
