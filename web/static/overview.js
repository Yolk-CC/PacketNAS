/* PocketNAS M13 总览页 —— 数据全部从现有端点前端聚合，无新增后端接口。
 * 端点：/api/system/info、/api/gallery/scan、/api/gallery?limit=1、
 *       /api/faces/status、/api/faces/persons、/api/settings/shares */
(function () {
  'use strict';

  var TOKEN_KEY = 'pocketnas_token';
  var $ = function (id) { return document.getElementById(id); };
  var toast = window.PocketToast;

  /* ---------- 工具 ---------- */
  function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }

  function fmtSize(n) {
    if (typeof n !== 'number' || isNaN(n)) return '–';
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
    return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB';
  }

  // 统一 fetch：自动带 token；401 切登录页
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
          throw new Error(msg);
        }
        return body;
      });
    });
  }

  /* ---------- 登录 ---------- */
  var loginView = $('login-view'), mainView = $('main-view');

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

  $('login-form').addEventListener('submit', function (e) {
    e.preventDefault();
    var btn = e.target.querySelector('button[type="submit"]');
    btn.classList.add('loading');
    btn.disabled = true;
    fetch('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: $('login-password').value })
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
    }).catch(function () {
      showLogin('网络异常，请稍后重试');
    }).then(function () {
      btn.classList.remove('loading');
      btn.disabled = false;
    });
  });

  $('login-eye').addEventListener('click', function () {
    var input = $('login-password');
    input.type = input.type === 'password' ? 'text' : 'password';
  });

  /* ---------- A. 存储卡 ---------- */
  var RING_LEN = 314.16; // 2πr, r=50

  function renderStorage(info) {
    var total = info.diskTotal || 0, free = info.diskFree || 0;
    var used = Math.max(total - free, 0);
    var pct = total > 0 ? used / total : 0;
    $('st-total').textContent = fmtSize(total);
    $('st-used').textContent = fmtSize(used);
    $('st-free').textContent = fmtSize(free);
    $('ring-pct').textContent = total > 0 ? Math.round(pct * 100) + '%' : '–';
    $('ring-fill').style.strokeDashoffset = String(RING_LEN * (1 - pct));
    var ring = $('storage-ring');
    ring.classList.toggle('warn', pct > 0.85 && pct <= 0.95);
    ring.classList.toggle('crit', pct > 0.95);
    $('storage-alert').classList.toggle('hidden', pct <= 0.95);
  }

  function cardError(cardId, retryFn) {
    var card = $(cardId);
    var old = card.querySelector('.card-error');
    if (old) old.remove();
    var div = document.createElement('div');
    div.className = 'card-error';
    div.innerHTML = '<svg class="icon" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="m9 9 6 6M15 9l-6 6"/></svg><span>数据加载失败</span>';
    var btn = document.createElement('button');
    btn.className = 'btn sm';
    btn.textContent = '重试';
    btn.addEventListener('click', retryFn);
    div.appendChild(btn);
    card.appendChild(div);
  }

  function loadSystem() {
    return api('/api/system/info').then(function (info) {
      renderStorage(info);
      var err = $('card-storage').querySelector('.card-error');
      if (err) err.remove();
      $('sv-name').textContent = info.serverName || '未命名';
      $('sv-version').textContent = 'v' + (info.version || '?');
      $('sv-root').textContent = info.root || '–';
      $('sv-addr').textContent = location.host;
      $('sv-go').textContent = info.goVersion || '–';
      if (info.serverName) $('ov-subtitle').textContent = info.serverName + ' 运行中';
    }).catch(function (e) {
      if (e.message === 'unauthorized') return;
      cardError('card-storage', loadSystem);
      cardError('card-server', loadSystem);
    });
  }

  $('sv-addr').addEventListener('click', function () {
    var text = location.host;
    function ok() { toast('已复制 ' + text, 'success'); }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(ok, function () { toast('复制失败', 'error'); });
    } else {
      var ta = document.createElement('textarea');
      ta.value = text;
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand('copy'); ok(); } catch (e) { toast('复制失败', 'error'); }
      ta.remove();
    }
  });

  /* ---------- C/D/E 统计卡 ---------- */
  function loadStats() {
    api('/api/gallery?offset=0&limit=1&type=image').then(function (b) {
      $('stat-photos-num').textContent = (b.total || 0).toLocaleString();
    }).catch(function () { $('stat-photos-num').textContent = '–'; });
    api('/api/gallery?offset=0&limit=1&type=video').then(function (b) {
      $('stat-videos-num').textContent = (b.total || 0).toLocaleString();
    }).catch(function () { $('stat-videos-num').textContent = '–'; });
    // persons 列表在引擎不可用时返回 503，先用 status 兜底数量
    api('/api/faces/status').then(function (st) {
      $('stat-people-num').textContent = (st.persons || 0).toLocaleString();
      return api('/api/faces/persons').then(function (list) {
        var named = (list || []).filter(function (p) { return !!p.name; }).length;
        $('stat-people-sub').textContent = '已命名 ' + named + ' 人';
      }).catch(function () {});
    }).catch(function () {
      $('stat-people-num').textContent = '–';
    });
  }

  $('stat-photos').addEventListener('click', function () { location.href = 'library.html?tab=photos'; });
  $('stat-videos').addEventListener('click', function () { location.href = 'library.html?tab=photos&type=video'; });
  $('stat-people').addEventListener('click', function () { location.href = 'library.html?tab=people'; });

  /* ---------- F. 任务卡 ---------- */
  var pollTimer = null;

  function taskRow(name, iconSvg, statusBadge, pct, subText, actionBtn) {
    var row = document.createElement('div');
    row.className = 'task-row';
    row.innerHTML =
      '<span class="task-icon">' + iconSvg + '</span>' +
      '<span class="task-name"></span>' +
      '<div class="task-mid">' +
      '  <div class="progress' + (pct === null ? ' indeterminate' : pct >= 100 ? ' done' : '') + '">' +
      '    <div class="track"><div class="fill" style="width:' + (pct === null ? 40 : pct) + '%"></div></div>' +
      (pct === null ? '' : '<span class="pct">' + Math.round(pct) + '%</span>') +
      '  </div>' +
      (subText ? '<div class="task-sub"></div>' : '') +
      '</div>' +
      '<div class="task-ops"><span class="badge ' + statusBadge.cls + '">' + statusBadge.text + '</span></div>';
    row.querySelector('.task-name').textContent = name;
    if (subText) row.querySelector('.task-sub').textContent = subText;
    if (actionBtn) row.querySelector('.task-ops').appendChild(actionBtn);
    return row;
  }

  var ICON_SCAN = '<svg class="icon" viewBox="0 0 24 24"><circle cx="11" cy="11" r="7"/><path d="m21 21-4.35-4.35"/></svg>';
  var ICON_FACE = '<svg class="icon" viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="2"/><path d="M9 9h.01M15 9h.01M9 15c1.5 1 4.5 1 6 0"/></svg>';

  function refreshTasks() {
    var rows = [];
    var galleryP = api('/api/gallery/scan').then(function (s) {
      if (s.scanning) {
        rows.push(taskRow('相册扫描', ICON_SCAN, { cls: 'warning', text: '进行中' }, null,
          '正在索引新照片… 已索引 ' + (s.indexed || 0) + ' 项'));
      } else if ((s.indexed || 0) > 0) {
        rows.push(taskRow('相册扫描', ICON_SCAN, { cls: 'success', text: '已完成' }, 100,
          '共索引 ' + (s.indexed || 0) + ' 项'));
      }
    }).catch(function () {});

    var facesP = api('/api/faces/status').then(function (st) {
      var q = st.queue || {};
      var navBadge = $('nav-faces-badge');
      if (q.pending > 0) {
        navBadge.textContent = q.pending;
        navBadge.classList.remove('hidden');
      } else {
        navBadge.classList.add('hidden');
      }
      if (!st.available) {
        rows.push(taskRow('人脸识别', ICON_FACE, { cls: 'neutral', text: '未就绪' }, 0,
          st.reason ? '原因：' + st.reason : '到识别中心下载模型后开始'));
        return;
      }
      if (q.scanning || q.pending > 0) {
        var totalQ = (q.done || 0) + (q.pending || 0);
        var pct = totalQ > 0 ? (q.done || 0) / totalQ * 100 : null;
        rows.push(taskRow('人脸识别', ICON_FACE, { cls: 'warning', text: '进行中' }, pct,
          '已处理 ' + (q.done || 0) + ' / ' + totalQ + ' 张'));
      } else if ((st.facesTotal || 0) > 0) {
        var btn = document.createElement('button');
        btn.className = 'btn sm';
        btn.textContent = '开始识别';
        btn.addEventListener('click', function () {
          api('/api/faces/scan', { method: 'POST' }).then(function () {
            toast('识别已开始', 'success');
            refreshTasks();
          }).catch(function (e) { toast('无法开始识别：' + e.message, 'error'); });
        });
        rows.push(taskRow('人脸识别', ICON_FACE, { cls: 'success', text: '空闲' }, 100,
          '已识别 ' + (st.facesTotal || 0) + ' 张人脸 / ' + (st.persons || 0) + ' 个人物', btn));
      }
    }).catch(function () {});

    Promise.all([galleryP, facesP]).then(function () {
      var list = $('tasks-list');
      list.innerHTML = '';
      rows.forEach(function (r) { list.appendChild(r); });
      $('tasks-empty').classList.toggle('hidden', rows.length > 0);
    });
  }

  $('btn-tasks-refresh').addEventListener('click', refreshTasks);

  function startPoll() {
    stopPoll();
    pollTimer = setInterval(function () {
      if (!document.hidden) refreshTasks();
    }, 3000);
  }
  function stopPoll() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  }
  document.addEventListener('visibilitychange', function () {
    if (!document.hidden && !mainView.classList.contains('hidden')) refreshTasks();
  });

  /* ---------- 共享条数（副标题补充） ---------- */
  function loadShares() {
    api('/api/settings/shares').then(function (body) {
      var n = (body && body.shares ? body.shares.length : 0);
      if (n > 0) {
        var el = $('ov-subtitle');
        if (el.textContent.indexOf('共享') === -1) {
          el.textContent += ' · ' + n + ' 个共享目录';
        }
      }
    }).catch(function () {});
  }

  /* ---------- 启动 ---------- */
  function enterMain() {
    showMain();
    loadSystem();
    loadStats();
    loadShares();
    refreshTasks();
    startPoll();
  }

  // 启动时试探：401 → 登录页（无提示文案）；成功 → 主视图
  fetch('/api/system/info', { headers: { 'X-Auth-Token': getToken() } })
    .then(function (res) {
      if (res.status === 401) { showLogin(); return; }
      enterMain();
    })
    .catch(function () {
      // 网络异常：仍进入主视图（各卡片自带错误态与重试）
      enterMain();
    });
})();
