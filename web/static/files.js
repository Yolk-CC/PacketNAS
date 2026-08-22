/* PocketNAS M13 文件页 —— 自 M1 app.js 迁入，功能逻辑不变：
 * 列表 / 面包屑 / 类型筛选 / 上传（含拖拽）/ 下载 / 行内重命名 / 删除 / 新建文件夹。
 * 变化：SVG 图标替换 emoji、toast 走 PocketToast、删除/新建用模态框、401 跳总览登录。 */
(function () {
  'use strict';

  var TOKEN_KEY = 'pocketnas_token';
  var state = {
    path: '/',        // 形如 "/" 或 "/sub/dir"
    typeFilter: 'all' // all | image | video
  };

  /* ---------- DOM ---------- */
  var $ = function (id) { return document.getElementById(id); };
  var breadcrumbEl = $('breadcrumb'), fileListEl = $('file-list');
  var listArea = $('list-area');
  var emptyHint = $('empty-hint'), noShareHint = $('no-share-hint'), dropHint = $('drop-hint');
  var toast = window.PocketToast;

  /* ---------- 工具 ---------- */
  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  function encodePath(p) {
    return p.split('/').filter(Boolean).map(encodeURIComponent).join('/');
  }

  function joinPath(base, name) {
    if (base === '/') return '/' + name;
    return base + '/' + name;
  }

  function formatSize(n, isDir) {
    if (isDir) return '—';
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
    return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB';
  }

  function formatTime(ts) {
    if (!ts) return '—';
    var d = new Date(ts * 1000);
    function p(x) { return (x < 10 ? '0' : '') + x; }
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) +
      ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
  }

  /* 文件类型图标（内联 SVG，stroke currentColor） */
  var TYPE_ICONS = {
    folder: '<svg class="icon" viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>',
    image: '<svg class="icon" viewBox="0 0 24 24"><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="9" cy="9" r="2"/><path d="m21 15-4.5-4.5L6 21"/></svg>',
    video: '<svg class="icon" viewBox="0 0 24 24"><polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2"/></svg>',
    audio: '<svg class="icon" viewBox="0 0 24 24"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>',
    doc: '<svg class="icon" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6M8 13h8M8 17h5"/></svg>',
    archive: '<svg class="icon" viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="5" rx="1"/><path d="M5 9v10a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V9M10 13h4"/></svg>',
    file: '<svg class="icon" viewBox="0 0 24 24"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/></svg>'
  };

  function iconFor(entry) {
    if (entry.isDir) return TYPE_ICONS.folder;
    var mime = entry.mimeType || '';
    var name = (entry.name || '').toLowerCase();
    if (mime.indexOf('image/') === 0) return TYPE_ICONS.image;
    if (mime.indexOf('video/') === 0) return TYPE_ICONS.video;
    if (mime.indexOf('audio/') === 0) return TYPE_ICONS.audio;
    if (mime.indexOf('text/') === 0 || /\.(md|txt|pdf|docx?|xlsx?|pptx?)$/.test(name)) return TYPE_ICONS.doc;
    if (/\.(zip|tar|gz|tgz|rar|7z|bz2|xz)$/.test(name)) return TYPE_ICONS.archive;
    return TYPE_ICONS.file;
  }

  /* ---------- 鉴权与请求封装 ---------- */
  function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }

  function backToLogin() {
    window.location.href = 'overview.html';
  }

  function api(url, options) {
    options = options || {};
    options.headers = options.headers || {};
    options.headers['X-Auth-Token'] = getToken();
    return fetch(url, options).then(function (res) {
      if (res.status === 401) {
        backToLogin();
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

  /* ---------- 模态框 helpers ---------- */
  var lastFocus = null;

  function openModal(modal, focusEl) {
    lastFocus = document.activeElement;
    modal.classList.remove('hidden');
    (focusEl || modal.querySelector('button')).focus();
  }
  function closeModal(modal) {
    modal.classList.add('hidden');
    if (lastFocus && lastFocus.focus) lastFocus.focus();
  }
  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    ['confirm-modal', 'prompt-modal'].forEach(function (id) {
      var m = $(id);
      if (!m.classList.contains('hidden')) closeModal(m);
    });
  });

  function confirmDialog(title, desc, okLabel, onOk) {
    $('confirm-title').textContent = title;
    $('confirm-desc').textContent = desc;
    $('confirm-ok').textContent = okLabel;
    var modal = $('confirm-modal');
    var okBtn = $('confirm-ok');
    var newOk = okBtn.cloneNode(true);
    okBtn.parentNode.replaceChild(newOk, okBtn);
    newOk.addEventListener('click', function () { closeModal(modal); onOk(); });
    $('confirm-cancel').onclick = function () { closeModal(modal); };
    openModal(modal, newOk);
  }

  function promptDialog(title, placeholder, okLabel, onOk) {
    $('prompt-title').textContent = title;
    var input = $('prompt-input');
    input.placeholder = placeholder || '';
    input.value = '';
    var modal = $('prompt-modal');
    $('prompt-ok').textContent = okLabel;
    $('prompt-ok').onclick = function () {
      var v = input.value.trim();
      if (!v) return;
      closeModal(modal);
      onOk(v);
    };
    $('prompt-cancel').onclick = function () { closeModal(modal); };
    input.onkeydown = function (e) {
      if (e.key === 'Enter') $('prompt-ok').click();
    };
    openModal(modal, input);
  }

  /* ---------- 面包屑 ---------- */
  function renderBreadcrumb() {
    breadcrumbEl.innerHTML = '';
    var parts = state.path.split('/').filter(Boolean);
    var root = document.createElement('button');
    root.className = 'crumb';
    root.textContent = '根目录';
    root.addEventListener('click', function () { navigate('/'); });
    breadcrumbEl.appendChild(root);
    parts.forEach(function (part, i) {
      var sep = document.createElement('span');
      sep.className = 'crumb-sep';
      sep.textContent = '/';
      breadcrumbEl.appendChild(sep);
      var btn = document.createElement('button');
      btn.className = 'crumb';
      btn.textContent = part;
      (function (idx) {
        btn.addEventListener('click', function () {
          navigate('/' + parts.slice(0, idx + 1).join('/'));
        });
      })(i);
      breadcrumbEl.appendChild(btn);
    });
  }

  /* ---------- 文件列表 ---------- */
  var itemCount = 0;

  function navigate(path) {
    state.path = path || '/';
    renderBreadcrumb();
    loadList();
  }

  function loadList() {
    var url = '/api/files?path=' + encodeURIComponent(state.path) +
      '&type=' + encodeURIComponent(state.typeFilter);
    api(url).then(function (entries) {
      renderList(entries || []);
    }).catch(function () {});
  }

  function renderList(entries) {
    fileListEl.innerHTML = '';
    itemCount = entries.length;
    emptyHint.classList.toggle('hidden', entries.length > 0);
    noShareHint.classList.add('hidden');
    entries.sort(function (a, b) {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      return a.name.localeCompare(b.name);
    });
    entries.forEach(function (entry) {
      var tr = document.createElement('tr');

      var nameTd = document.createElement('td');
      var nameBtn = document.createElement('button');
      nameBtn.className = 'name-cell';
      nameBtn.innerHTML = '<span class="icon">' + iconFor(entry) + '</span>' +
        '<span class="name">' + escapeHtml(entry.name) + '</span>';
      nameBtn.addEventListener('click', function () {
        if (entry.isDir) navigate(joinPath(state.path, entry.name));
        else downloadFile(entry);
      });
      nameTd.appendChild(nameBtn);

      var sizeTd = document.createElement('td');
      sizeTd.textContent = formatSize(entry.size, entry.isDir);
      var timeTd = document.createElement('td');
      timeTd.className = 'col-time';
      timeTd.textContent = formatTime(entry.modified);

      var opsTd = document.createElement('td');
      opsTd.className = 'ops-col';
      opsTd.appendChild(opBtn('下载', function () { downloadFile(entry); }));
      opsTd.appendChild(opBtn('重命名', function () { startInlineRename(nameTd, entry); }));
      opsTd.appendChild(opBtn('删除', function () { deleteEntry(entry); }, true));

      tr.appendChild(nameTd); tr.appendChild(sizeTd);
      tr.appendChild(timeTd); tr.appendChild(opsTd);
      fileListEl.appendChild(tr);
    });
    updateStatus();
  }

  function opBtn(label, fn, danger) {
    var b = document.createElement('button');
    b.className = 'op-btn' + (danger ? ' danger' : '');
    b.textContent = label;
    b.addEventListener('click', function (e) { e.stopPropagation(); fn(); });
    return b;
  }

  /* ---------- 行内重命名（Enter 确认 / Esc 取消） ---------- */
  function startInlineRename(nameTd, entry) {
    var old = nameTd.querySelector('.name-cell');
    var input = document.createElement('input');
    input.className = 'input rename-input';
    input.value = entry.name;
    nameTd.replaceChild(input, old);
    input.focus();
    input.select();
    var done = false;
    function restore() {
      if (!done) { done = true; loadList(); }
    }
    input.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') {
        var v = input.value.trim();
        if (!v || v === entry.name) { restore(); return; }
        done = true;
        api('/api/files/rename', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ path: entry.path, newName: v })
        }).then(function () { toast('已重命名', 'success'); loadList(); })
          .catch(function () { loadList(); });
      } else if (e.key === 'Escape') {
        restore();
      }
    });
    input.addEventListener('blur', restore);
  }

  /* ---------- 下载 ---------- */
  function downloadFile(entry) {
    var url = '/api/download/' + encodePath(entry.path);
    if (entry.isDir) url += '?archive=zip'; // 目录打包 ZIP（SPEC 3.4）
    fetch(url, { headers: { 'X-Auth-Token': getToken() } }).then(function (res) {
      if (res.status === 401) { backToLogin(); return null; }
      if (!res.ok) {
        return res.json().then(function (b) {
          toast((b.error && b.error.message) || '下载失败', 'error');
        });
      }
      return res.blob().then(function (blob) {
        var a = document.createElement('a');
        var fname = entry.isDir ? entry.name + '.zip' : entry.name;
        a.href = URL.createObjectURL(blob);
        a.download = fname;
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(function () { URL.revokeObjectURL(a.href); }, 1000);
      });
    }).catch(function () { toast('下载失败', 'error'); });
  }

  /* ---------- 删除 / 新建 ---------- */
  function deleteEntry(entry) {
    confirmDialog(
      '删除' + (entry.isDir ? '目录' : '文件'),
      '确定删除「' + entry.name + '」吗？此操作不可恢复' + (entry.isDir ? '（目录将递归删除）' : ''),
      '删除',
      function () {
        api('/api/files', {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ paths: [entry.path] })
        }).then(function () { toast('已删除', 'success'); loadList(); }).catch(function () {});
      }
    );
  }

  $('btn-mkdir').addEventListener('click', function () {
    promptDialog('新建文件夹', '文件夹名称', '创建', function (name) {
      api('/api/files/mkdir', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ dir: state.path, name: name })
      }).then(function () { toast('已创建', 'success'); loadList(); }).catch(function () {});
    });
  });

  /* ---------- 上传 ---------- */
  var fileInput = $('file-input');
  $('btn-upload').addEventListener('click', function () { fileInput.click(); });
  fileInput.addEventListener('change', function () {
    if (fileInput.files.length) uploadFiles(fileInput.files);
    fileInput.value = '';
  });

  function uploadFiles(files) {
    var fd = new FormData();
    for (var i = 0; i < files.length; i++) fd.append('file', files[i]);
    toast('正在上传 ' + files.length + ' 个文件…', 'info');
    api('/api/upload?path=' + encodeURIComponent(state.path), {
      method: 'POST',
      body: fd
    }).then(function (body) {
      toast('已上传 ' + (body.uploaded ? body.uploaded.length : files.length) + ' 个文件', 'success');
      loadList();
    }).catch(function () {});
  }

  // 拖拽上传到列表区域
  ['dragenter', 'dragover'].forEach(function (ev) {
    listArea.addEventListener(ev, function (e) {
      e.preventDefault();
      dropHint.classList.remove('hidden');
    });
  });
  ['dragleave', 'drop'].forEach(function (ev) {
    listArea.addEventListener(ev, function (e) {
      e.preventDefault();
      if (ev === 'dragleave' && e.relatedTarget && listArea.contains(e.relatedTarget)) return;
      dropHint.classList.add('hidden');
    });
  });
  listArea.addEventListener('drop', function (e) {
    if (e.dataTransfer && e.dataTransfer.files.length) uploadFiles(e.dataTransfer.files);
  });

  /* ---------- 类型筛选 ---------- */
  $('type-filter').addEventListener('change', function (e) {
    state.typeFilter = e.target.value;
    loadList();
  });

  /* ---------- 底部状态条 ---------- */
  var diskInfo = null;
  function updateStatus() {
    var parts = ['共 ' + itemCount + ' 项'];
    if (diskInfo) parts.push('剩余空间 ' + formatSize(diskInfo.diskFree, false));
    $('sysinfo').textContent = parts.join(' · ');
  }

  function loadSysInfo() {
    api('/api/system/info').then(function (info) {
      diskInfo = info;
      updateStatus();
    }).catch(function () {});
  }

  /* ---------- 启动 ---------- */
  api('/api/files?path=' + encodeURIComponent('/') + '&type=all')
    .then(function () {
      navigate('/');
      loadSysInfo();
    })
    .catch(function (err) {
      if (err && err.message === 'unauthorized') return; // 已跳总览登录
      // 其他错误（如无共享的 legacy 提示不在此处）仍尝试渲染空态
      navigate('/');
    });
})();
