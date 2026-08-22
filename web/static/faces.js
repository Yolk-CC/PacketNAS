/* PocketNAS M13 识别中心 —— 自旧设置页人脸卡片迁入 library.html?tab=faces。
 * 功能不变：模型档位 / 下载 / 应用 / 开始识别 / 进度轮询 / 导出 / 导入。
 * API 契约见 SPEC-M11 §3（internal/faces/handlers.go）。 */
(function () {
  'use strict';

  var TOKEN_KEY = 'pocketnas_token';
  var $ = function (id) { return document.getElementById(id); };
  var toast = window.PocketToast || function () {};
  var pollTimer = null;
  var selectedProfile = null;
  var lastStatus = null;

  /* 档位静态描述（精度 / 速度 / 体积） */
  var PROFILE_META = {
    buffalo_l: { desc: '高精度 · 较慢 · 约 300MB' },
    buffalo_s: { desc: '均衡 · 推荐', fallback: '均衡' },
    mobilefacenet: { desc: '轻量 · 最快 · 体积最小' }
  };

  function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }

  function api(url, options) {
    options = options || {};
    options.headers = options.headers || {};
    options.headers['X-Auth-Token'] = getToken();
    return fetch(url, options).then(function (res) {
      if (res.status === 401) {
        location.href = 'overview.html';
        throw new Error('unauthorized');
      }
      var ct = res.headers.get('Content-Type') || '';
      var bodyPromise = ct.indexOf('application/json') !== -1 ? res.json() : res.text();
      return bodyPromise.then(function (body) {
        if (!res.ok) {
          var msg = (body && body.error && body.error.message) || ('HTTP ' + res.status);
          throw new Error(msg);
        }
        return body;
      });
    });
  }

  function fmtBytes(n) {
    if (n >= (1 << 20)) return (n / (1 << 20)).toFixed(1) + ' MB';
    if (n >= 1024) return (n / 1024).toFixed(1) + ' KB';
    return n + ' B';
  }

  /* ---------- 档位卡片 ---------- */
  function profileDownloaded(st, name) {
    var prof = (st.profiles || {})[name];
    if (!prof) return false;
    var models = st.models || [];
    return models.indexOf(prof.detModel) !== -1 && models.indexOf(prof.recModel) !== -1;
  }

  function renderProfiles(st) {
    var wrap = $('profile-cards');
    wrap.innerHTML = '';
    var names = Object.keys(st.profiles || {});
    if (!names.length) names = ['buffalo_s', 'buffalo_l', 'mobilefacenet'];
    if (!selectedProfile) selectedProfile = (st.model && st.model.profile) || 'buffalo_s';
    names.forEach(function (name) {
      var meta = PROFILE_META[name] || { desc: '' };
      var card = document.createElement('div');
      card.className = 'card interactive profile-card' + (name === selectedProfile ? ' selected' : '');
      card.setAttribute('role', 'radio');
      card.setAttribute('aria-checked', name === selectedProfile ? 'true' : 'false');
      card.tabIndex = 0;
      var html = '<span class="p-check"><svg class="icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="m8.5 12.5 2.5 2.5 5-5.5"/></svg></span>' +
        '<div class="p-name"></div>' +
        '<div class="p-desc">' + meta.desc + '</div>';
      card.innerHTML = html;
      card.querySelector('.p-name').textContent = name;
      if (profileDownloaded(st, name)) {
        var b = document.createElement('span');
        b.className = 'badge success';
        b.textContent = '已下载';
        card.appendChild(b);
      }
      card.addEventListener('click', function () {
        selectedProfile = name;
        renderProfiles(st);
        updateApplyState(st);
      });
      wrap.appendChild(card);
    });
  }

  function updateApplyState(st) {
    var applyBtn = $('btn-faces-apply');
    var hint = $('faces-apply-hint');
    var ok = selectedProfile && profileDownloaded(st, selectedProfile);
    applyBtn.disabled = !ok;
    hint.classList.toggle('hidden', !!ok);
  }

  /* ---------- 状态渲染 ---------- */
  function renderFaces(st) {
    lastStatus = st;
    var m = st.model || {};
    $('faces-current-profile').textContent = m.profile || '未配置';
    $('faces-models').textContent = st.available
      ? ('运行库已加载 · 已识别 ' + (st.facesTotal || 0) + ' 张人脸 / ' + (st.persons || 0) + ' 个人物')
      : ('不可用：' + (st.reason || '模型或运行库未就绪'));

    renderProfiles(st);
    updateApplyState(st);

    // 首次使用提示：模型一个都没下载且从未识别
    var first = !st.available && !(st.models || []).length && !(st.facesTotal > 0);
    $('faces-first-hint').classList.toggle('hidden', !first);

    // 下载进度
    var d = st.download || {};
    var dlRow = $('faces-download-row');
    var dlFill = dlRow.querySelector('.fill');
    var dlBar = $('faces-download-progress-bar');
    if (d.downloading) {
      dlRow.classList.remove('hidden');
      dlBar.classList.remove('failed');
      var pct = d.total > 0 ? Math.min(100, d.bytes / d.total * 100) : null;
      if (pct === null) {
        dlBar.classList.add('indeterminate');
        $('faces-download-pct').textContent = '';
      } else {
        dlBar.classList.remove('indeterminate');
        dlFill.style.width = pct + '%';
        $('faces-download-pct').textContent = Math.round(pct) + '%';
      }
      $('faces-download-text').textContent = '下载中：' + (d.file || '') + ' ' +
        fmtBytes(d.bytes || 0) + (d.total > 0 ? ' / ' + fmtBytes(d.total) : '');
      $('btn-faces-download').classList.add('loading');
      $('btn-faces-download').disabled = true;
    } else if (d.error) {
      dlRow.classList.remove('hidden');
      dlBar.classList.remove('indeterminate');
      dlBar.classList.add('failed');
      dlFill.style.width = '100%';
      $('faces-download-pct').textContent = '';
      $('faces-download-text').textContent = '下载失败，请检查网络后重试（' + d.error + '）';
      $('btn-faces-download').classList.remove('loading');
      $('btn-faces-download').disabled = false;
      $('btn-faces-download').textContent = '重试下载';
    } else {
      dlRow.classList.add('hidden');
      $('btn-faces-download').classList.remove('loading');
      $('btn-faces-download').disabled = false;
    }

    // 识别队列
    var q = st.queue || {};
    var badge = $('faces-queue-badge');
    var fill = $('faces-queue-fill');
    var pctEl = $('faces-queue-pct');
    var textEl = $('faces-queue-text');
    var scanBtn = $('btn-faces-scan');
    var bar = $('faces-queue-progress');
    var pending = q.pending || 0;

    // 导航/页签徽标
    ['nav-faces-badge', 'tab-faces-badge'].forEach(function (id) {
      var el = $(id);
      if (!el) return;
      if (pending > 0) { el.textContent = pending; el.classList.remove('hidden'); }
      else el.classList.add('hidden');
    });

    if (q.scanning || pending > 0) {
      var totalQ = (q.done || 0) + pending;
      var qpct = totalQ > 0 ? (q.done || 0) / totalQ * 100 : 0;
      badge.className = 'badge warning';
      badge.textContent = '进行中';
      bar.classList.remove('done', 'failed');
      fill.style.width = qpct + '%';
      pctEl.textContent = Math.round(qpct) + '%';
      textEl.textContent = '已处理 ' + (q.done || 0) + ' / ' + totalQ + ' 张';
      scanBtn.disabled = true;
    } else {
      var done = (st.facesTotal || 0) > 0;
      badge.className = 'badge ' + (done ? 'success' : 'neutral');
      badge.textContent = done ? '空闲' : '空闲';
      bar.classList.toggle('done', done);
      fill.style.width = done ? '100%' : '0';
      pctEl.textContent = done ? '100%' : '';
      textEl.textContent = done
        ? ('共识别 ' + (st.facesTotal || 0) + ' 张人脸 · ' + (st.persons || 0) + ' 个人物')
        : (st.available ? '尚未开始识别' : '需先下载并应用模型');
      scanBtn.disabled = !st.available;
    }
  }

  function pollFaces() {
    api('/api/faces/status').then(renderFaces).catch(function (e) {
      if (e.message === 'unauthorized') return;
      $('faces-queue-text').textContent = '状态查询失败';
    });
  }

  function startFacesPoll() {
    if (!pollTimer) pollTimer = setInterval(function () {
      if (!document.hidden) pollFaces();
    }, 2000);
  }

  /* ---------- 操作 ---------- */
  $('btn-faces-download').addEventListener('click', function () {
    api('/api/faces/models/download', { method: 'POST' }).then(function () {
      toast('开始下载，请稍候…', 'info');
      startFacesPoll();
      pollFaces();
    }).catch(function (e) { toast('下载失败：' + e.message, 'error'); });
  });

  $('btn-faces-apply').addEventListener('click', function () {
    var btn = this;
    btn.classList.add('loading');
    btn.disabled = true;
    api('/api/faces/models', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ profile: selectedProfile })
    }).then(function (st) {
      renderFaces(st);
      toast(st.available
        ? '已切换到 ' + selectedProfile + '，新识别将使用此模型'
        : '模型未就绪：' + (st.reason || ''), st.available ? 'success' : 'error');
    }).catch(function (e) {
      toast('应用失败：' + e.message, 'error');
    }).then(function () {
      btn.classList.remove('loading');
    });
  });

  $('btn-faces-scan').addEventListener('click', function () {
    api('/api/faces/scan', { method: 'POST' }).then(function () {
      toast('识别已开始', 'success');
      startFacesPoll();
      pollFaces();
    }).catch(function (e) { toast('无法开始识别：' + e.message, 'error'); });
  });

  $('btn-faces-export').addEventListener('click', function () {
    var btn = this;
    btn.classList.add('loading');
    btn.disabled = true;
    fetch('/api/faces/export', { headers: { 'X-Auth-Token': getToken() } })
      .then(function (res) {
        if (!res.ok) throw new Error('HTTP ' + res.status);
        return res.blob();
      })
      .then(function (blob) {
        var a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = 'face-data.json.gz';
        a.click();
        URL.revokeObjectURL(a.href);
        toast('已导出 face-data.json.gz', 'success');
      })
      .catch(function (e) { toast('导出失败：' + e.message, 'error'); })
      .then(function () {
        btn.classList.remove('loading');
        btn.disabled = false;
      });
  });

  var pendingImportFile = null;
  $('btn-faces-import').addEventListener('click', function () {
    $('faces-import-file').click();
  });
  $('faces-import-file').addEventListener('change', function (ev) {
    var f = ev.target.files[0];
    ev.target.value = '';
    if (!f) return;
    pendingImportFile = f;
    $('import-desc').textContent = '将导入「' + f.name + '」，数据会合并到现有人物与人脸中。';
    $('import-modal').classList.remove('hidden');
    $('import-ok').focus();
  });
  $('import-cancel').addEventListener('click', function () {
    pendingImportFile = null;
    $('import-modal').classList.add('hidden');
  });
  $('import-ok').addEventListener('click', function () {
    var f = pendingImportFile;
    if (!f) return;
    var btn = this;
    btn.classList.add('loading');
    btn.disabled = true;
    api('/api/faces/import', { method: 'POST', headers: { 'Content-Type': 'application/gzip' }, body: f })
      .then(function (res) {
        toast('导入完成：人物 ' + (res.persons || 0) + '，人脸 ' + (res.faces || 0) +
          '，跳过重复 ' + (res.skipped || 0), 'success');
        $('import-modal').classList.add('hidden');
        pendingImportFile = null;
        pollFaces();
      })
      .catch(function (e) { toast('导入失败：' + e.message, 'error'); })
      .then(function () {
        btn.classList.remove('loading');
        btn.disabled = false;
      });
  });

  /* ---------- 启动 ---------- */
  pollFaces();
  startFacesPoll();
})();
