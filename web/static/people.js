/* PocketNAS M11 人物页 —— 人物网格 / 人物照片 / 命名 / 合并。
 * 原生 fetch，无框架；API 契约见 SPEC-M11 §3。 */
(function () {
  'use strict';

  var TOKEN_KEY = 'pocketnas_token';
  var $ = function (id) { return document.getElementById(id); };
  var toastEl = $('toast');

  var state = {
    persons: [],
    current: null // 当前查看的人物 id
  };

  function toast(msg, isError) {
    toastEl.textContent = msg;
    toastEl.className = 'toast' + (isError ? ' error' : '');
    clearTimeout(toastEl._timer);
    toastEl._timer = setTimeout(function () { toastEl.classList.add('hidden'); }, 3500);
  }

  function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }

  function api(url, options) {
    options = options || {};
    options.headers = options.headers || {};
    options.headers['X-Auth-Token'] = getToken();
    return fetch(url, options).then(function (res) {
      if (res.status === 401) {
        location.href = 'index.html';
        throw new Error('unauthorized');
      }
      return res.json().then(function (body) {
        if (!res.ok) {
          var msg = (body && body.error && body.error.message) || ('HTTP ' + res.status);
          throw new Error(msg);
        }
        return body;
      });
    });
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  /* ---------- 状态行 ---------- */
  function refreshStatus() {
    api('/api/faces/status').then(function (st) {
      var q = st.queue || {};
      if (!st.available) {
        $('status-text').textContent = '人脸识别不可用：' + (st.reason || '') + '（到设置页下载模型）';
      } else if (q.scanning || q.pending > 0) {
        $('status-text').textContent = '识别中… 已完成 ' + (q.done || 0) + '，待处理 ' + (q.pending || 0);
      } else {
        $('status-text').textContent = '共 ' + (st.persons || 0) + ' 个人物 · ' + (st.facesTotal || 0) + ' 张人脸';
      }
    }).catch(function () {
      $('status-text').textContent = '状态查询失败';
    });
  }

  /* ---------- 人物网格 ---------- */
  function loadPersons() {
    api('/api/faces/persons').then(function (list) {
      state.persons = list || [];
      renderPeople();
    }).catch(function (e) {
      $('people-empty').classList.remove('hidden');
      $('people-empty').textContent = '人物加载失败：' + e.message;
    });
  }

  function renderPeople() {
    var grid = $('people-grid');
    grid.innerHTML = '';
    $('people-empty').classList.toggle('hidden', state.persons.length > 0);
    state.persons.forEach(function (p) {
      var cell = document.createElement('button');
      cell.className = 'cell person-cell';
      var img = document.createElement('img');
      img.loading = 'lazy';
      img.src = p.coverUrl || 'placeholder.svg';
      img.alt = p.name || '未命名';
      img.onerror = function () { cell.classList.add('thumb-error'); img.remove(); };
      cell.appendChild(img);
      var label = document.createElement('div');
      label.className = 'person-label';
      label.innerHTML = '<span class="person-name">' + escapeHtml(p.name || '未命名') + '</span>' +
        '<span class="person-count">' + p.faceCount + ' 张</span>';
      cell.appendChild(label);
      cell.addEventListener('click', function () { openPerson(p.id); });
      grid.appendChild(cell);
    });
  }

  /* ---------- 人物详情 ---------- */
  function openPerson(id) {
    state.current = id;
    var p = state.persons.filter(function (x) { return x.id === id; })[0];
    $('people-view').classList.add('hidden');
    $('person-view').classList.remove('hidden');
    $('person-name').textContent = p ? (p.name || '未命名') : '';
    $('person-name-input').classList.add('hidden');
    $('person-name-input').value = p ? (p.name || '') : '';
    $('btn-rename').textContent = '命名';
    // 合并目标 = 其他人物
    var sel = $('merge-target');
    sel.innerHTML = '';
    state.persons.forEach(function (x) {
      if (x.id === id) return;
      var opt = document.createElement('option');
      opt.value = x.id;
      opt.textContent = x.name || ('人物 #' + x.id);
      sel.appendChild(opt);
    });
    api('/api/faces/persons/' + id + '/photos').then(function (res) {
      renderPhotos(res.items || []);
    }).catch(function (e) {
      toast('照片加载失败：' + e.message, true);
    });
  }

  function renderPhotos(items) {
    var grid = $('photo-grid');
    grid.innerHTML = '';
    $('photo-empty').classList.toggle('hidden', items.length > 0);
    items.forEach(function (m) {
      var cell = document.createElement('button');
      cell.className = 'cell';
      var img = document.createElement('img');
      img.loading = 'lazy';
      img.src = m.thumbUrl;
      img.alt = m.name;
      img.onerror = function () { cell.classList.add('thumb-error'); img.remove(); };
      cell.appendChild(img);
      cell.addEventListener('click', function () {
        // 原图需鉴权：带 token 取回 blob 再打开
        var url = '/api/media/file' + m.path.split('/').map(encodeURIComponent).join('/');
        fetch(url, { headers: { 'X-Auth-Token': getToken() } })
          .then(function (res) {
            if (!res.ok) throw new Error('HTTP ' + res.status);
            return res.blob();
          })
          .then(function (blob) { window.open(URL.createObjectURL(blob), '_blank'); })
          .catch(function (e) { toast('打开原图失败：' + e.message, true); });
      });
      grid.appendChild(cell);
    });
  }

  /* ---------- 命名 / 合并 ---------- */
  $('btn-rename').addEventListener('click', function () {
    var input = $('person-name-input');
    if (input.classList.contains('hidden')) {
      input.classList.remove('hidden');
      input.focus();
      $('btn-rename').textContent = '保存';
      return;
    }
    var name = input.value.trim();
    api('/api/faces/persons/' + state.current, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: name })
    }).then(function (list) {
      state.persons = list || [];
      var p = state.persons.filter(function (x) { return x.id === state.current; })[0];
      $('person-name').textContent = p ? (p.name || '未命名') : name;
      input.classList.add('hidden');
      $('btn-rename').textContent = '命名';
      toast('已保存');
    }).catch(function (e) { toast('命名失败：' + e.message, true); });
  });

  $('btn-merge').addEventListener('click', function () {
    var to = parseInt($('merge-target').value, 10);
    if (!to || !state.current) { toast('请选择合并目标', true); return; }
    if (!confirm('确认将当前人物合并到所选人物？此操作不可撤销。')) return;
    api('/api/faces/persons/merge', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ from: state.current, to: to })
    }).then(function (list) {
      state.persons = list || [];
      toast('已合并');
      backToPeople();
    }).catch(function (e) { toast('合并失败：' + e.message, true); });
  });

  function backToPeople() {
    state.current = null;
    $('person-view').classList.add('hidden');
    $('people-view').classList.remove('hidden');
    loadPersons();
  }
  $('btn-back').addEventListener('click', backToPeople);

  $('btn-rescan').addEventListener('click', function () {
    api('/api/faces/scan', { method: 'POST' }).then(function () {
      toast('识别已开始');
      refreshStatus();
    }).catch(function (e) { toast('无法开始识别：' + e.message, true); });
  });
  $('btn-logout').addEventListener('click', function () {
    localStorage.removeItem(TOKEN_KEY);
    location.href = 'index.html';
  });

  /* ---------- 启动 ---------- */
  refreshStatus();
  setInterval(refreshStatus, 3000);
  loadPersons();
})();
