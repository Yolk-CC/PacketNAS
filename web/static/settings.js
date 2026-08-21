/* PocketNAS M7 设置页 —— 共享路径管理。原生 fetch，无框架，无外部依赖。
 * API 契约见 SPEC-M7 §3。 */
(function () {
  'use strict';

  var TOKEN_KEY = 'pocketnas_token';
  var state = {
    shares: [],   // [{name, path}]
    browsePath: '' // 目录选择器当前路径（'' 表示系统根）
  };

  /* ---------- DOM ---------- */
  var $ = function (id) { return document.getElementById(id); };
  var shareListEl = $('share-list'), emptyHint = $('empty-hint');
  var legacyHint = $('legacy-hint'), toastEl = $('toast');
  var modal = $('dir-modal'), dirListEl = $('dir-list');
  var dirEmpty = $('dir-empty'), browsePathEl = $('browse-path');

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

  function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }

  // 统一 fetch：自动带 token；401 跳回文件页（沿用现有鉴权模式）；错误体 toast
  function api(url, options) {
    options = options || {};
    options.headers = options.headers || {};
    options.headers['X-Auth-Token'] = getToken();
    return fetch(url, options).then(function (res) {
      if (res.status === 401) {
        window.location.href = 'index.html';
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
      pathTd.textContent = share.path;

      var opsTd = document.createElement('td');
      opsTd.className = 'ops-col';
      var delBtn = document.createElement('button');
      delBtn.className = 'op-btn danger';
      delBtn.textContent = '删除';
      delBtn.addEventListener('click', function () {
        state.shares.splice(i, 1);
        renderShares();
      });
      opsTd.appendChild(delBtn);

      tr.appendChild(nameTd); tr.appendChild(pathTd); tr.appendChild(opsTd);
      shareListEl.appendChild(tr);
    });
  }

  $('btn-add').addEventListener('click', function () {
    var name = $('share-name').value.trim();
    var path = $('share-path').value.trim();
    if (!name) { toast('请输入共享名称', true); return; }
    if (!path) { toast('请选择目录', true); return; }
    if (name.indexOf('/') !== -1 || name === '.' || name === '..') {
      toast('名称不能包含 /', true); return;
    }
    for (var i = 0; i < state.shares.length; i++) {
      if (state.shares[i].name === name) { toast('名称已存在', true); return; }
    }
    state.shares.push({ name: name, path: path });
    $('share-name').value = '';
    $('share-path').value = '';
    renderShares();
  });

  /* ---------- 保存 ---------- */
  $('btn-save').addEventListener('click', function () {
    var status = $('save-status');
    api('/api/settings/shares', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ shares: state.shares })
    }).then(function (body) {
      state.shares = (body && body.shares) || state.shares;
      renderShares();
      status.textContent = '已保存';
      toast('共享配置已保存');
      setTimeout(function () { status.textContent = ''; }, 3000);
    }).catch(function () { status.textContent = ''; });
  });

  /* ---------- 目录选择器 ---------- */
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
      var entries = (body && body.dirs) || (Array.isArray(body) ? body : []);
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
      li.innerHTML = '<span class="icon">\uD83D\uDCC1</span>' + escapeHtml(entry.Name || entry.name);
      li.addEventListener('click', function () { loadDir(entry.Path || entry.path); });
      dirListEl.appendChild(li);
    });
  }

  $('btn-browse').addEventListener('click', openModal);
  $('btn-cancel').addEventListener('click', closeModal);
  $('btn-up').addEventListener('click', function () {
    var p = state.browsePath;
    if (!p) return;
    // 去掉末尾一段返回上级；到根则回到 ''（系统根/盘符列表）
    var idx = p.replace(/[\\/]+$/, '').lastIndexOf('/');
    if (idx === -1) idx = p.lastIndexOf('\\');
    var parent = idx > 0 ? p.substring(0, idx) : '';
    loadDir(parent);
  });
  $('btn-choose').addEventListener('click', function () {
    if (!state.browsePath) { toast('请先进入一个目录', true); return; }
    $('share-path').value = state.browsePath;
    closeModal();
  });

  /* ---------- 启动 ---------- */
  // 无密码模式下 token 可能从未设置；以真实 API 探测为准，仅 401 才跳回文件页
  api('/api/settings/shares').then(function () {
    loadShares();
  }).catch(function (e) {
    if (e && e.message === 'unauthorized') return; // api() 已处理跳转
    loadShares();
  });

  /* ---------- M11: 人脸识别卡片 ---------- */
  var facesStatus = $('faces-status'), facesModels = $('faces-models');
  var facesProgress = $('faces-progress'), facesDlProg = $('faces-download-progress');
  var facesPollTimer = null;

  function fmtBytes(n) {
    if (n >= (1 << 20)) return (n / (1 << 20)).toFixed(1) + ' MB';
    if (n >= 1024) return (n / 1024).toFixed(1) + ' KB';
    return n + ' B';
  }

  function renderFaces(st) {
    if (st.available) {
      facesStatus.textContent = '可用 · 运行库已加载 · 已识别 ' + (st.facesTotal || 0) + ' 张人脸 / ' +
        (st.persons || 0) + ' 个人物';
      facesStatus.className = 'muted';
    } else {
      facesStatus.textContent = '不可用：' + (st.reason || '未知原因');
      facesStatus.className = 'muted error-text';
    }
    var m = st.model || {};
    facesModels.textContent = '当前模型：' + (m.profile || '-') + '（检测 ' + (m.det || '-') +
      '，特征 ' + (m.rec || '-') + '，' + (m.dims || 0) + ' 维）· 目录内模型：' +
      ((st.models || []).join('、') || '无');
    if (st.profiles && st.model && st.model.profile && $('faces-profile').value !== st.model.profile) {
      $('faces-profile').value = st.model.profile;
    }
    var q = st.queue || {};
    facesProgress.textContent = q.scanning || q.pending > 0
      ? ('识别中… 已完成 ' + (q.done || 0) + '，待处理 ' + (q.pending || 0))
      : ((st.facesTotal || 0) > 0 ? '空闲' : '');
    var d = st.download || {};
    if (d.downloading) {
      facesDlProg.classList.remove('hidden');
      facesDlProg.textContent = '下载中：' + (d.file || '') + ' ' + fmtBytes(d.bytes || 0) +
        (d.total > 0 ? ' / ' + fmtBytes(d.total) : '');
    } else if (d.error) {
      facesDlProg.classList.remove('hidden');
      facesDlProg.textContent = '下载失败：' + d.error;
    } else {
      facesDlProg.classList.add('hidden');
    }
  }

  function pollFaces() {
    api('/api/faces/status').then(renderFaces).catch(function () {
      facesStatus.textContent = '状态查询失败';
    });
  }

  function startFacesPoll() {
    if (!facesPollTimer) facesPollTimer = setInterval(pollFaces, 2000);
  }

  $('btn-faces-download').addEventListener('click', function () {
    api('/api/faces/models/download', { method: 'POST' }).then(function () {
      toast('开始下载，请稍候…');
      startFacesPoll();
    }).catch(function (e) { toast('下载失败：' + e.message, true); });
  });

  $('btn-faces-apply').addEventListener('click', function () {
    api('/api/faces/models', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ profile: $('faces-profile').value })
    }).then(function (st) {
      renderFaces(st);
      toast(st.available ? '模型已应用' : '模型未就绪：' + (st.reason || ''));
    }).catch(function (e) { toast('应用失败：' + e.message, true); });
  });

  $('btn-faces-scan').addEventListener('click', function () {
    api('/api/faces/scan', { method: 'POST' }).then(function () {
      toast('识别已开始');
      startFacesPoll();
      pollFaces();
    }).catch(function (e) { toast('无法开始识别：' + e.message, true); });
  });

  $('btn-faces-export').addEventListener('click', function () {
    fetch('/api/faces/export', { headers: { 'X-Auth-Token': getToken() } })
      .then(function (res) {
        if (!res.ok) throw new Error('HTTP ' + res.status);
        return res.blob();
      })
      .then(function (blob) {
        var a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = 'faces-export.json.gz';
        a.click();
        URL.revokeObjectURL(a.href);
      })
      .catch(function (e) { toast('导出失败：' + e.message, true); });
  });

  $('btn-faces-import').addEventListener('click', function () {
    $('faces-import-file').click();
  });
  $('faces-import-file').addEventListener('change', function (ev) {
    var f = ev.target.files[0];
    if (!f) return;
    api('/api/faces/import', { method: 'POST', headers: { 'Content-Type': 'application/gzip' }, body: f })
      .then(function (res) {
        toast('导入完成：人物 ' + (res.persons || 0) + '，人脸 ' + (res.faces || 0) +
          '，跳过重复 ' + (res.skipped || 0));
        pollFaces();
      })
      .catch(function (e) { toast('导入失败：' + e.message, true); });
    ev.target.value = '';
  });

  pollFaces();
  startFacesPoll();
})();
