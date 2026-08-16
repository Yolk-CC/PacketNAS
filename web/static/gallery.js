/* PocketNAS M2/M3 相册页 —— 原生 fetch，无框架，无外部依赖。
 * M3：Live Photo 角标 + 灯箱悬停/长按播放（SPEC-M3 §1.3/§2）。
 * API 契约见 SPEC-M2 §5；鉴权沿用 M1（localStorage pocketnas_token + X-Auth-Token）。
 * 媒体（缩略图/原图/视频）均需鉴权，统一 fetch(blob) → URL.createObjectURL。 */
(function () {
  'use strict';

  var TOKEN_KEY = 'pocketnas_token';
  var PAGE_LIMIT = 200;
  var THUMB_CONCURRENCY = 6;

  var state = {
    typeFilter: 'all', // all | image | video
    items: [],         // 已加载的媒体项，按 API 返回顺序（takenTime 倒序）
    total: 0,
    loading: false,
    done: false,       // 已加载全部
    scanning: false
  };

  /* ---------- DOM ---------- */
  var $ = function (id) { return document.getElementById(id); };
  var gridEl = $('grid'), toastEl = $('toast');
  var emptyHint = $('empty-hint'), loadingHint = $('loading-hint'), endHint = $('end-hint');
  var scanText = $('scan-text');
  var lightbox = $('lightbox'), lbStage = $('lb-stage');
  var lbImg = $('lb-img'), lbVideo = $('lb-video');
  var lbLive = $('lb-live'), lbMute = $('lb-mute');
  var lbQuality = $('lb-quality'), lbOverlay = $('lb-overlay'), lbOverlayText = $('lb-overlay-text');
  var lbName = $('lb-name'), lbTime = $('lb-time'), lbPos = $('lb-pos');

  /* ---------- 工具 ---------- */
  function toast(msg, isError) {
    toastEl.textContent = msg;
    toastEl.className = 'toast' + (isError ? ' error' : '');
    clearTimeout(toastEl._timer);
    toastEl._timer = setTimeout(function () { toastEl.classList.add('hidden'); }, 3500);
  }

  function getToken() { return localStorage.getItem(TOKEN_KEY) || ''; }

  function backToLogin() {
    window.location.href = 'index.html';
  }

  // 将 "/DCIM/a.jpg" 形式的相对路径编码为 URL 片段（每段 encodeURIComponent）
  function encodePath(p) {
    return p.split('/').filter(Boolean).map(encodeURIComponent).join('/');
  }

  function formatTime(ts) {
    if (!ts) return '';
    var d = new Date(ts * 1000);
    function p(x) { return (x < 10 ? '0' : '') + x; }
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) +
      ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
  }

  function isVideo(item) {
    return (item.mimeType || '').indexOf('video/') === 0;
  }

  /* ---------- 请求封装 ---------- */
  // JSON API：自动带 token；401 跳回登录页
  function api(url) {
    return fetch(url, { headers: { 'X-Auth-Token': getToken() } }).then(function (res) {
      if (res.status === 401) { backToLogin(); throw new Error('unauthorized'); }
      return res.json().then(function (body) {
        if (!res.ok) {
          var msg = (body && body.error && body.error.message) || ('请求失败 (' + res.status + ')');
          toast(msg, true);
          throw new Error(msg);
        }
        return body;
      });
    });
  }

  // 媒体 fetch → objectURL（带 X-Auth-Token；img/video 标签无法带头）
  function fetchObjectURL(url) {
    return fetch(url, { headers: { 'X-Auth-Token': getToken() } }).then(function (res) {
      if (res.status === 401) { backToLogin(); throw new Error('unauthorized'); }
      if (!res.ok) throw new Error('media ' + res.status);
      return res.blob();
    }).then(function (blob) {
      return URL.createObjectURL(blob);
    });
  }

  /* ---------- 缩略图并发限制（最多 THUMB_CONCURRENCY 个并发 fetch） ---------- */
  var thumbRunning = 0;
  var thumbQueue = [];
  function thumbFetch(url) {
    return new Promise(function (resolve, reject) {
      thumbQueue.push({ url: url, resolve: resolve, reject: reject });
      pumpThumbs();
    });
  }
  function pumpThumbs() {
    while (thumbRunning < THUMB_CONCURRENCY && thumbQueue.length) {
      var job = thumbQueue.shift();
      thumbRunning++;
      fetchObjectURL(job.url).then(job.resolve, job.reject).then(function () {
        thumbRunning--;
        pumpThumbs();
      });
    }
  }

  /* ---------- 索引状态轮询 ---------- */
  function renderScanStatus() {
    scanText.textContent = '共 ' + state.total + ' 项 · ' +
      (state.scanning ? '索引中…（已索引 ' + state.indexed + '）' : '索引完成');
  }
  function pollScan() {
    api('/api/gallery/scan').then(function (body) {
      state.scanning = !!body.scanning;
      state.indexed = body.indexed || 0;
      renderScanStatus();
    }).catch(function () {});
  }
  setInterval(pollScan, 3000);

  /* ---------- 网格加载与渲染 ---------- */
  var thumbURLs = []; // 记录已创建的缩略图 objectURL，重建网格时统一释放

  function loadPage() {
    if (state.loading || state.done) return;
    state.loading = true;
    loadingHint.classList.remove('hidden');
    var url = '/api/gallery?offset=' + state.items.length +
      '&limit=' + PAGE_LIMIT +
      '&type=' + encodeURIComponent(state.typeFilter);
    api(url).then(function (body) {
      state.total = body.total || 0;
      renderScanStatus();
      var items = body.items || [];
      state.items = state.items.concat(items);
      if (items.length < PAGE_LIMIT || state.items.length >= state.total) state.done = true;
      renderGrid(items);
      updateHints();
    }).catch(function () {}).then(function () {
      state.loading = false;
      loadingHint.classList.add('hidden');
    });
  }

  function updateHints() {
    emptyHint.classList.toggle('hidden', state.items.length > 0);
    endHint.classList.toggle('hidden', !(state.done && state.items.length > 0));
  }

  // 追加渲染一页
  function renderGrid(items) {
    items.forEach(function (item) {
      var idx = state.items.indexOf(item);
      var cell = document.createElement('button');
      cell.className = 'cell';
      cell.title = item.name;

      var img = document.createElement('img');
      img.alt = item.name;
      img.loading = 'lazy';
      cell.appendChild(img);

      if (isVideo(item)) {
        var badge = document.createElement('span');
        badge.className = 'video-badge';
        badge.textContent = '▶';
        cell.appendChild(badge);
      }

      // M3：Live Photo 右上角「LIVE」胶囊角标
      if (item.isLivePhoto) {
        var liveBadge = document.createElement('span');
        liveBadge.className = 'live-badge';
        liveBadge.textContent = 'LIVE';
        cell.appendChild(liveBadge);
      }

      cell.addEventListener('click', function () { openLightbox(idx); });
      gridEl.appendChild(cell);

      // 缩略图：API 返回 thumbUrl（/api/thumb/<path>?w=300&h=300），需鉴权 → blob
      if (item.thumbUrl) {
        thumbFetch(item.thumbUrl).then(function (objURL) {
          thumbURLs.push(objURL);
          img.src = objURL;
        }).catch(function () {
          cell.classList.add('thumb-error');
        });
      }
    });
  }

  function resetAndReload() {
    // 释放全部缩略图 objectURL，避免内存泄漏
    thumbURLs.forEach(function (u) { URL.revokeObjectURL(u); });
    thumbURLs = [];
    state.items = [];
    state.done = false;
    state.loading = false;
    gridEl.innerHTML = '';
    loadPage();
  }

  /* ---------- 滚动到底自动加载下一页 ---------- */
  if ('IntersectionObserver' in window) {
    var io = new IntersectionObserver(function (entries) {
      if (entries[0].isIntersecting) loadPage();
    }, { rootMargin: '600px' });
    io.observe($('scroll-sentinel'));
  } else {
    window.addEventListener('scroll', function () {
      if (window.innerHeight + window.scrollY >= document.body.scrollHeight - 600) loadPage();
    });
  }

  /* ---------- 类型筛选 ---------- */
  $('type-filter').addEventListener('change', function (e) {
    state.typeFilter = e.target.value;
    resetAndReload();
  });

  $('btn-logout').addEventListener('click', function () {
    localStorage.removeItem(TOKEN_KEY);
    backToLogin();
  });

  /* ---------- 灯箱 ---------- */
  var lb = { index: -1, objURL: null, zoomed: false };

  function releaseLightboxURL() {
    if (lb.objURL) { URL.revokeObjectURL(lb.objURL); lb.objURL = null; }
  }

  /* ---------- M3：Live Photo 灯箱播放 ---------- */
  var LIVE_DELAY = 1500; // 悬停/长按 1.5s 后触发
  var live = {
    timer: null,    // 未触发的延迟定时器
    objURL: null,   // 正在播放的视频 objectURL
    playing: false,
    forIndex: -1,   // 本次播放/请求对应的灯箱条目（竞态防护）
    fetching: false
  };

  function currentItem() {
    return lb.index >= 0 && lb.index < state.items.length ? state.items[lb.index] : null;
  }
  function currentIsLivePhoto() {
    var it = currentItem();
    return !!(it && it.isLivePhoto && !isVideo(it));
  }

  function cancelLiveTimer() {
    if (live.timer) { clearTimeout(live.timer); live.timer = null; }
  }

  // 停止播放并恢复静态图（同时使进行中的 fetch 结果失效）
  function stopLive() {
    cancelLiveTimer();
    live.forIndex = -1;
    live.playing = false;
    lbLive.pause();
    lbLive.removeAttribute('src');
    lbLive.load();
    lbLive.classList.add('hidden');
    lbLive.classList.remove('playing');
    lbMute.classList.add('hidden');
    if (live.objURL) { URL.revokeObjectURL(live.objURL); live.objURL = null; }
  }

  // 开始加载并播放 /api/livephoto/<path>（fetch blob → objectURL，带鉴权）
  function playLive(item) {
    if (lb.index < 0 || state.items[lb.index] !== item) return; // 已切换
    stopLive(); // 清理旧状态
    live.forIndex = lb.index;
    live.fetching = true;
    fetchObjectURL('/api/livephoto/' + encodePath(item.path)).then(function (objURL) {
      live.fetching = false;
      // 播放期间已切换条目/已停止 → 丢弃
      if (live.forIndex !== lb.index || lb.index < 0) { URL.revokeObjectURL(objURL); return; }
      live.objURL = objURL;
      live.playing = true;
      lbLive.muted = true;
      lbMute.innerHTML = '&#128263;';
      lbMute.classList.remove('unmuted');
      lbMute.classList.remove('hidden');
      lbLive.src = objURL;
      lbLive.classList.remove('hidden');
      lbLive.play().catch(function () {});
      // 淡入在 loadeddata 后触发，避免黑帧闪烁
    }).catch(function () {
      // 加载失败静默降级：保持静态图，不弹错
      live.fetching = false;
      live.forIndex = -1;
    });
  }
  lbLive.addEventListener('loadeddata', function () {
    if (live.playing) lbLive.classList.add('playing');
  });
  lbLive.addEventListener('error', function () {
    stopLive(); // 解码失败静默回退静态图
  });

  function armLiveTimer() {
    cancelLiveTimer();
    if (!currentIsLivePhoto() || live.playing) return;
    var item = currentItem();
    live.timer = setTimeout(function () {
      live.timer = null;
      playLive(item);
    }, LIVE_DELAY);
  }

  // 静音/取消静音切换
  lbMute.addEventListener('click', function (e) {
    e.stopPropagation();
    lbLive.muted = !lbLive.muted;
    lbMute.innerHTML = lbLive.muted ? '&#128263;' : '&#128266;';
    lbMute.classList.toggle('unmuted', !lbLive.muted);
  });
  // PC：悬停 1.5s 播放，移出停止（在 img 与视频覆盖层之间移动不算移出）
  function mediaHoverZone(el) { return el === lbImg || el === lbLive; }
  lbImg.addEventListener('mouseenter', function () { armLiveTimer(); });
  lbImg.addEventListener('mouseleave', function (e) {
    if (mediaHoverZone(e.relatedTarget)) return;
    stopLive();
  });
  lbLive.addEventListener('mouseleave', function (e) {
    if (mediaHoverZone(e.relatedTarget)) return;
    stopLive();
  });

  /* ---------- M4：视频清晰度切换（转码） ---------- */
  var RES_LABELS = { original: '原画', '1080p': '1080p', '720p': '720p', '360p': '360p' };
  var RES_ORDER = ['original', '1080p', '720p', '360p'];
  var vq = { seq: 0, pollTimer: null }; // seq = 序号失效法；pollTimer = 转码状态轮询

  // 停止转码相关的一切：使进行中的请求/轮询失效
  function stopVideoQuality() {
    vq.seq++;
    if (vq.pollTimer) { clearTimeout(vq.pollTimer); vq.pollTimer = null; }
    lbOverlay.classList.add('hidden');
  }

  function showTranscodeOverlay(progress) {
    lbOverlayText.textContent = '转码中 ' + Math.round(progress || 0) + '%';
    lbOverlay.classList.remove('hidden');
  }

  // 视频实际起播（竞态已由调用方校验）
  function setVideoSource(objURL) {
    releaseLightboxURL();
    lb.objURL = objURL;
    lbOverlay.classList.add('hidden');
    lbImg.classList.add('hidden');
    lbVideo.classList.remove('hidden');
    lbVideo.src = objURL;
    lbVideo.play().catch(function () {});
  }

  // 转码失败：toast 并回退原画
  function transcodeFailed(item) {
    lbOverlay.classList.add('hidden');
    toast('转码失败，已切回原画', true);
    lbQuality.value = 'original';
    playQuality(item, 'original');
  }

  // 播放指定清晰度：original 走 /api/media/file；转码档走 /api/video/<path>?res=xxx
  function playQuality(item, res) {
    stopVideoQuality();
    var mySeq = vq.seq;
    var myIndex = lb.index;
    function stale() { return vq.seq !== mySeq || lb.index !== myIndex; }

    if (res === 'original') {
      fetchObjectURL('/api/media/file/' + encodePath(item.path)).then(function (objURL) {
        if (stale()) { URL.revokeObjectURL(objURL); return; }
        setVideoSource(objURL);
      }).catch(function () {
        if (!stale()) toast('媒体加载失败', true);
      });
      return;
    }

    var url = '/api/video/' + encodePath(item.path) + '?res=' + encodeURIComponent(res);
    fetch(url, { headers: { 'X-Auth-Token': getToken() } }).then(function (r) {
      if (r.status === 401) { backToLogin(); throw new Error('unauthorized'); }
      if (stale()) throw new Error('stale');
      if (r.status === 200) {
        return r.blob().then(function (b) {
          var objURL = URL.createObjectURL(b);
          if (stale()) { URL.revokeObjectURL(objURL); return; }
          setVideoSource(objURL);
        });
      }
      if (r.status === 202) { // queued/running：显示进度并轮询
        return r.json().then(function (body) {
          showTranscodeOverlay(body.progress || 0);
          pollTranscode(item, res, mySeq, myIndex);
        });
      }
      if (r.status === 409) { transcodeFailed(item); return; }
      throw new Error('video ' + r.status);
    }).catch(function (err) {
      if (err && (err.message === 'stale' || err.message === 'unauthorized')) return;
      if (!stale()) transcodeFailed(item);
    });
  }

  // 每 2s 轮询 GET /api/video/status/<path>?res=xxx，done 后重新 fetch 播放
  function pollTranscode(item, res, mySeq, myIndex) {
    vq.pollTimer = setTimeout(function () {
      var url = '/api/video/status/' + encodePath(item.path) + '?res=' + encodeURIComponent(res);
      fetch(url, { headers: { 'X-Auth-Token': getToken() } }).then(function (r) {
        if (r.status === 401) { backToLogin(); return; }
        if (vq.seq !== mySeq || lb.index !== myIndex) return; // 已切换/关闭
        if (!r.ok) { transcodeFailed(item); return; }
        return r.json().then(function (body) {
          if (vq.seq !== mySeq || lb.index !== myIndex) return;
          if (body.status === 'done') {
            playQuality(item, res); // 重新 GET，此时应 200
          } else if (body.status === 'failed') {
            transcodeFailed(item);
          } else {
            showTranscodeOverlay(body.progress || 0);
            pollTranscode(item, res, mySeq, myIndex);
          }
        });
      }).catch(function () {
        // 网络抖动：序号仍有效则继续轮询
        if (vq.seq === mySeq && lb.index === myIndex) pollTranscode(item, res, mySeq, myIndex);
      });
    }, 2000);
  }

  // 依据 item.resolutions 构建清晰度选择器（默认原画）
  function setupQualitySelector(item) {
    var available = item.resolutions && item.resolutions.length ? item.resolutions : RES_ORDER;
    lbQuality.innerHTML = '';
    RES_ORDER.forEach(function (res) {
      if (available.indexOf(res) === -1) return;
      var opt = document.createElement('option');
      opt.value = res;
      opt.textContent = RES_LABELS[res] || res;
      lbQuality.appendChild(opt);
    });
    lbQuality.value = 'original';
    lbQuality.classList.remove('hidden');
  }

  lbQuality.addEventListener('change', function () {
    var item = currentItem();
    if (item && isVideo(item)) playQuality(item, lbQuality.value);
  });

  function setZoom(zoomed, e) {
    lb.zoomed = zoomed;
    if (zoomed) {
      if (e && lbImg.getBoundingClientRect) {
        var r = lbImg.getBoundingClientRect();
        var x = ((e.clientX - r.left) / r.width) * 100;
        var y = ((e.clientY - r.top) / r.height) * 100;
        lbImg.style.transformOrigin = x + '% ' + y + '%';
      }
      lbImg.classList.add('zoomed');
    } else {
      lbImg.classList.remove('zoomed');
    }
  }

  function showItem(i) {
    if (i < 0 || i >= state.items.length) return;
    lb.index = i;
    var item = state.items[i];

    // 释放上一项资源（含未触发的 Live Photo 定时器与播放、转码轮询）
    stopLive();
    stopVideoQuality();
    releaseLightboxURL();
    lbVideo.pause();
    lbVideo.removeAttribute('src');
    lbVideo.load();
    lbImg.src = '';
    setZoom(false);

    lbName.textContent = item.name;
    lbTime.textContent = formatTime(item.takenTime);
    lbPos.textContent = (i + 1) + '/' + state.total;

    if (isVideo(item)) {
      // M4：视频项显示清晰度选择器，默认原画（/api/media/file）
      setupQualitySelector(item);
      playQuality(item, 'original');
      return;
    }
    lbQuality.classList.add('hidden');

    // 原图：GET /api/media/file/<path>（每段 encodeURIComponent），需鉴权 → blob
    fetchObjectURL('/api/media/file/' + encodePath(item.path)).then(function (objURL) {
      if (lb.index !== i) { URL.revokeObjectURL(objURL); return; } // 已切换，丢弃
      lb.objURL = objURL;
      lbVideo.classList.add('hidden');
      lbImg.classList.remove('hidden');
      lbImg.src = objURL;
    }).catch(function () { toast('媒体加载失败', true); });
  }

  function navStep(delta) {
    var next = lb.index + delta;
    if (next >= state.items.length && !state.done) {
      // 到达已加载末尾但还有更多：先加载下一页再跳转
      loadPage();
      var check = setInterval(function () {
        if (!state.loading) {
          clearInterval(check);
          if (next < state.items.length) showItem(next);
        }
      }, 150);
      return;
    }
    if (next < 0) next = 0;
    if (next >= state.items.length) next = state.items.length - 1;
    showItem(next);
  }

  function openLightbox(i) {
    lightbox.classList.remove('hidden');
    document.body.style.overflow = 'hidden';
    showItem(i);
  }

  function closeLightbox() {
    lightbox.classList.add('hidden');
    document.body.style.overflow = '';
    stopLive();
    stopVideoQuality();
    lbVideo.pause();
    lbVideo.removeAttribute('src');
    lbVideo.load();
    lbImg.src = '';
    releaseLightboxURL();
    lb.index = -1;
  }

  $('lb-close').addEventListener('click', closeLightbox);
  $('lb-prev').addEventListener('click', function () { navStep(-1); });
  $('lb-next').addEventListener('click', function () { navStep(1); });

  // 点击图片外背景关闭
  lbStage.addEventListener('click', function (e) {
    if (e.target === lbStage) closeLightbox();
  });

  // 键盘：←/→ 切换，Esc 关闭
  document.addEventListener('keydown', function (e) {
    if (lightbox.classList.contains('hidden')) return;
    if (e.key === 'Escape') closeLightbox();
    else if (e.key === 'ArrowLeft') navStep(-1);
    else if (e.key === 'ArrowRight') navStep(1);
  });

  // 双击放大/还原（仅图片）
  lbImg.addEventListener('dblclick', function (e) {
    setZoom(!lb.zoomed, e);
  });

  // 触摸手势：左右滑动切换，下滑关闭，双击（轻点两次）缩放，长按 1.5s 播放 Live Photo
  var touch = null;
  var lastTap = 0;
  lbStage.addEventListener('touchstart', function (e) {
    if (e.touches.length === 1) {
      touch = { x: e.touches[0].clientX, y: e.touches[0].clientY, t: Date.now() };
      // 长按播放：仅当落在静态图/播放层上且当前项为 Live Photo
      if ((e.target === lbImg || e.target === lbLive) && currentIsLivePhoto()) armLiveTimer();
    }
  }, { passive: true });
  lbStage.addEventListener('touchmove', function (e) {
    if (!touch) return;
    var dx = e.touches[0].clientX - touch.x;
    var dy = e.touches[0].clientY - touch.y;
    // 移动超过 10px 视为滑动：取消长按定时器；若已在播放也停止，让位滑动手势
    if (Math.abs(dx) > 10 || Math.abs(dy) > 10) stopLive();
  }, { passive: true });
  lbStage.addEventListener('touchend', function (e) {
    stopLive(); // 松手/触结束：取消未触发的长按定时器并停止播放
    if (!touch) return;
    var dx = e.changedTouches[0].clientX - touch.x;
    var dy = e.changedTouches[0].clientY - touch.y;
    touch = null;
    var adx = Math.abs(dx), ady = Math.abs(dy);
    if (adx > 60 && adx > ady * 1.5) {
      navStep(dx < 0 ? 1 : -1); // 左滑下一张，右滑上一张
    } else if (dy > 80 && ady > adx * 1.5) {
      closeLightbox();          // 下滑关闭
    } else if (adx < 12 && ady < 12 && e.target === lbImg) {
      // 双击（两次轻点）放大/还原
      var now = Date.now();
      if (now - lastTap < 350) {
        var t = e.changedTouches[0];
        setZoom(!lb.zoomed, { clientX: t.clientX, clientY: t.clientY });
        lastTap = 0;
      } else {
        lastTap = now;
      }
    }
  }, { passive: true });
  lbStage.addEventListener('touchcancel', function () {
    touch = null;
    stopLive();
  }, { passive: true });

  /* ---------- 启动 ---------- */
  // 无密码模式下 token 可能从未设置；以真实 API 探测为准，仅 401 才跳回登录页
  api('/api/gallery?offset=0&limit=1&type=all').then(function () {
    pollScan();
    loadPage();
  }).catch(function (e) {
    if (e && e.message === 'unauthorized') return; // api() 内已 backToLogin()
    pollScan(); // 其他错误（如网络抖动）仍尝试进入
    loadPage();
  });
})();
