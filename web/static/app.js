/* PocketNAS M1 Web 文件管理器 —— 原生 fetch，无框架，无外部依赖。
 * API 契约见 SPEC §3。 */
(function () {
  'use strict';

  var TOKEN_KEY = 'pocketnas_token';
  var state = {
    path: '/',        // 形如 "/" 或 "/sub/dir"
    typeFilter: 'all' // all | image | video
  };

  /* ---------- DOM ---------- */
  var $ = function (id) { return document.getElementById(id); };
  var loginView = $('login-view'), mainView = $('main-view');
  var breadcrumbEl = $('breadcrumb'), fileListEl = $('file-list');
  var toastEl = $('toast'), listArea = $('list-area');
  var emptyHint = $('empty-hint'), dropHint = $('drop-hint');

  /* ---------- 工具 ---------- */
  function toast(msg, isError) {
    toastEl.textContent = msg;
    toastEl.className = 'toast' + (isError ? ' error' : '');
    clearTimeout(toastEl._timer);
    toastEl._timer = setTimeout(function () { toastEl.classList.add('hidden'); }, 3500);
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  // 将 "/sub/dir" 形式的相对路径编码为 URL 片段（每段 encodeURIComponent）
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

  function iconFor(entry) {
    if (entry.isDir) return '\uD83D\uDCC1';            // 📁
    var mime = entry.mimeType || '';
    if (mime.indexOf('image/') === 0) return '\uD83D\uDDBC\uFE0F'; // 🖼️
    if (mime.indexOf('video/') === 0) return '\uD83C\uDFAC';       // 🎬
    return '\uD83D\uDCC4';                              // 📄
  }

  /* ---------- 鉴权与请求封装 ---------- */
  function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }

  function showLogin(msg) {
    mainView.classList.add('hidden');
    loginView.classList.remove('hidden');
    var err = $('login-error');
    if (msg) { err.textContent = msg; err.classList.remove('hidden'); }
    else err.classList.add('hidden');
  }

  function showMain() {
    loginView.classList.add('hidden');
    mainView.classList.remove('hidden');
  }

  // 统一 fetch：自动带 token；401 切登录页；error 体 toast
  function api(url, options) {
    options = options || {};
    options.headers = options.headers || {};
    options.headers['X-Auth-Token'] = getToken();
    return fetch(url, options).then(function (res) {
      if (res.status === 401) {
        showLogin('登录已过期，请重新登录');
        throw new Error('unauthorized');
      }
      var ct = res.headers.get('Content-Type') || '';
      var bodyPromise = ct.indexOf('application/json') !== -1 ? res.json() : res.text();
      return bodyPromise.then(function (body) {
        if (!res.ok) {
          var msg = (body && body.error && body.error.message) || ('请求失败 (' + res.status + ')');
          toast(msg, true);
          throw new Error(msg);
        }
        return body;
      });
    });
  }

  /* ---------- 登录 ---------- */
  $('login-form').addEventListener('submit', function (e) {
    e.preventDefault();
    var pwd = $('login-password').value;
    fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: pwd })
    }).then(function (res) {
      return res.json().then(function (body) {
        if (!res.ok) {
          showLogin((body.error && body.error.message) || '登录失败');
          return;
        }
        localStorage.setItem(TOKEN_KEY, body.token || '');
        $('login-password').value = '';
        enterMain();
      });
    }).catch(function () { showLogin('网络错误'); });
  });

  $('btn-logout').addEventListener('click', function () {
    localStorage.removeItem(TOKEN_KEY);
    showLogin();
  });

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
    emptyHint.classList.toggle('hidden', entries.length > 0);
    // 目录排前、名称排序（服务端已排序，这里兜底）
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
      timeTd.textContent = formatTime(entry.modified);

      var opsTd = document.createElement('td');
      opsTd.className = 'ops-col';
      opsTd.appendChild(opBtn('下载', function () { downloadFile(entry); }));
      opsTd.appendChild(opBtn('重命名', function () { renameEntry(entry); }));
      opsTd.appendChild(opBtn('删除', function () { deleteEntry(entry); }, true));

      tr.appendChild(nameTd); tr.appendChild(sizeTd);
      tr.appendChild(timeTd); tr.appendChild(opsTd);
      fileListEl.appendChild(tr);
    });
  }

  function opBtn(label, fn, danger) {
    var b = document.createElement('button');
    b.className = 'op-btn' + (danger ? ' danger' : '');
    b.textContent = label;
    b.addEventListener('click', function (e) { e.stopPropagation(); fn(); });
    return b;
  }

  /* ---------- 下载 ---------- */
  function downloadFile(entry) {
    var url = '/api/download/' + encodePath(entry.path);
    if (entry.isDir) url += '?archive=zip'; // 目录打包 ZIP（SPEC 3.4）
    // 下载接口也需要 X-Auth-Token，故用 fetch + blob 触发保存。
    fetch(url, { headers: { 'X-Auth-Token': getToken() } }).then(function (res) {
      if (res.status === 401) { showLogin('登录已过期，请重新登录'); return null; }
      if (!res.ok) {
        return res.json().then(function (b) {
          toast((b.error && b.error.message) || '下载失败', true);
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
    }).catch(function () { toast('下载失败', true); });
  }

  /* ---------- 文件操作 ---------- */
  function renameEntry(entry) {
    var newName = window.prompt('新名称：', entry.name);
    if (!newName || newName === entry.name) return;
    api('/api/files/rename', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: entry.path, newName: newName })
    }).then(function () { toast('已重命名'); loadList(); }).catch(function () {});
  }

  function deleteEntry(entry) {
    if (!window.confirm('确认删除「' + entry.name + '」？' + (entry.isDir ? '（目录将递归删除）' : ''))) return;
    api('/api/files', {
      method: 'DELETE',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ paths: [entry.path] })
    }).then(function () { toast('已删除'); loadList(); }).catch(function () {});
  }

  $('btn-mkdir').addEventListener('click', function () {
    var name = window.prompt('新文件夹名称：');
    if (!name) return;
    api('/api/files/mkdir', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ dir: state.path, name: name })
    }).then(function () { toast('已创建'); loadList(); }).catch(function () {});
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
    api('/api/upload?path=' + encodeURIComponent(state.path), {
      method: 'POST',
      body: fd
    }).then(function (body) {
      toast('已上传 ' + (body.uploaded ? body.uploaded.length : files.length) + ' 个文件');
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

  /* ---------- 系统信息 ---------- */
  function loadSysInfo() {
    api('/api/system/info').then(function (info) {
      $('sysinfo').textContent = 'v' + info.version +
        ' · 可用 ' + formatSize(info.diskFree, false) +
        ' / ' + formatSize(info.diskTotal, false);
    }).catch(function () {});
  }

  /* ---------- 启动 ---------- */
  function enterMain() {
    showMain();
    navigate('/');
    loadSysInfo();
  }

  // 启动时试探一次列表：401 → 登录页；成功 → 主视图
  api('/api/files?path=' + encodeURIComponent('/') + '&type=all')
    .then(function () { enterMain(); })
    .catch(function (err) {
      if (err && err.message === 'unauthorized') return; // showLogin 已触发
      showLogin();
    });
})();
