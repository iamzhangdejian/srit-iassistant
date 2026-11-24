/* 国研智研体 · 授权管理（管理端 SPA）
 *
 * 设计约束：
 * - 不引第三方框架/构建步骤，单个 app.js 由 go:embed 打包进二进制，
 *   双击启动的 .app 里没有 node_modules 可用。
 * - 身份是服务端会话 Cookie（账号密码登录，阶段 2）；401 时回到登录闸门。
 *   自动化令牌（X-Licadmin-Token）只用于脚本，浏览器里不再默认走它。
 * - 视图用 hash 路由（#/licenses 等），刷新后仍停在原页。
 */
(function () {
  "use strict";

  var TOKEN_KEY = "licadmin_token";
  var THEME_KEY = "gy_admin_theme";
  var FEATURES = [
    { key: "kb", label: "知识库" },
    { key: "chat", label: "对话问答" },
    { key: "ocr", label: "OCR 识别" },
    { key: "office", label: "文档生成" }
  ];
  var PAGE_SIZE = 20;

  var state = {
    token: "",
    auth: { enabled: false, setupRequired: false, authenticated: false, username: "", method: "" },
    meta: null,
    route: "overview",
    licenses: { keyword: "", status: "", offset: 0, total: 0, rows: [] }
  };

  // ---------- 基础工具 ----------

  function $(sel, root) { return (root || document).querySelector(sel); }
  function $$(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }
  function esc(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function pad(n) { return n < 10 ? "0" + n : "" + n; }

  function fmtTime(unix) {
    if (!unix) return "永久";
    var d = new Date(unix * 1000);
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()) +
      " " + pad(d.getHours()) + ":" + pad(d.getMinutes());
  }
  function fmtDay(unix) {
    if (!unix) return "永久";
    var d = new Date(unix * 1000);
    return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate());
  }
  function daysLeft(exp) {
    if (!exp) return Infinity;
    return Math.ceil((exp * 1000 - Date.now()) / 86400000);
  }
  function featureLabel(k) {
    for (var i = 0; i < FEATURES.length; i++) if (FEATURES[i].key === k) return FEATURES[i].label;
    return k;
  }
  function featureText(list) {
    if (!list || !list.length) return "全部";
    return list.map(featureLabel).join(" · ");
  }

  function toast(msg) {
    var el = $("#toast");
    el.textContent = msg;
    el.hidden = false;
    clearTimeout(toast._t);
    toast._t = setTimeout(function () { el.hidden = true; }, 1600);
  }

  function copyText(text, label) {
    if (!text) return;
    var done = function () { toast((label || "内容") + "已复制"); };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(done, function () { fallback(text); done(); });
      return;
    }
    fallback(text);
    done();
  }
  function fallback(text) {
    var ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand("copy"); } catch (e) { /* 忽略：剪贴板不可用 */ }
    document.body.removeChild(ta);
  }

  // ---------- 口令与请求 ----------

  function tokenFromUrl() {
    var m = /[?&]t=([^&]+)/.exec(location.search);
    return m ? decodeURIComponent(m[1]) : "";
  }

  function bootToken() {
    var fromUrl = tokenFromUrl();
    var stored = "";
    try { stored = sessionStorage.getItem(TOKEN_KEY) || ""; } catch (e) { stored = ""; }
    state.token = fromUrl || stored;
    if (fromUrl) {
      try { sessionStorage.setItem(TOKEN_KEY, fromUrl); } catch (e) { /* 隐私模式 */ }
      // 把口令从地址栏摘掉：截图、复制链接时不该带凭据。
      history.replaceState(null, "", location.pathname + location.hash);
    }
  }

  function askToken(message) {
    openModal({
      title: "输入访问口令",
      body:
        '<div class="field"><label>访问口令</label>' +
        '<input type="text" id="token-input" class="mono" placeholder="启动时终端里输出的口令，或「访问口令.txt」" autocomplete="off"></div>' +
        '<p class="hint">' + esc(message || "口令用于保护发码权限，不会写进链接。") + "</p>",
      actions: [
        { label: "确定", primary: true, onClick: function () {
          var v = ($("#token-input") && $("#token-input").value || "").trim();
          if (!v) { toast("请先填入口令"); return true; }
          state.token = v;
          try { sessionStorage.setItem(TOKEN_KEY, v); } catch (e) { /* 忽略 */ }
          closeModal();
          route();
          return false;
        } }
      ],
      onOpen: function () { var el = $("#token-input"); if (el) el.focus(); }
    });
  }

  // ---------- 登录闸门（账号密码，阶段 2） ----------

  // authFetch 不走 api()：登录/初始化接口本身返回 401/423，不能触发
  // 「重新登录」弹窗，否则失败一次就自我循环。
  // authFetch：带 Cookie 的请求。
  //
  // opts.raw 时不解析 JSON、直接返回 Response——备份是 zip、恢复要上传文件，
  // 把它们塞进 JSON 通道只会绕一大圈。
  function authFetch(path, body, opts) {
    opts = opts || {};
    var headers = {};
    var payload;
    if (opts.body instanceof Blob || typeof opts.body === "string") {
      // 二进制/文件：Content-Type 交给浏览器按文件类型设置，别强行 JSON
      payload = opts.body;
    } else {
      headers["Content-Type"] = "application/json";
      payload = JSON.stringify(body || {});
    }
    return fetch(path, {
      method: opts.method || "POST",
      headers: headers,
      body: payload
    }).then(function (res) {
      if (opts.raw) return res;
      return res.json().catch(function () { return {}; }).then(function (data) {
        return { ok: res.ok, status: res.status, data: data };
      });
    });
  }

  function loadAuthStatus() {
    var headers = {};
    if (state.token) headers["X-Licadmin-Token"] = state.token;
    return fetch("/api/auth/status", { headers: headers })
      .then(function (res) { return res.json(); })
      .then(function (st) {
        state.auth = {
          enabled: !!st.auth_enabled,
          setupRequired: !!st.setup_required,
          authenticated: !!st.authenticated,
          username: st.username || "",
          method: st.method || ""
        };
        return state.auth;
      });
  }

  function hideGate() {
    var gate = $("#gate");
    gate.hidden = true;
    document.body.classList.remove("is-locked");
  }

  function showGate(mode) {
    var gate = $("#gate");
    var card = $("#gate-card");
    document.body.classList.add("is-locked");
    gate.hidden = false;

    if (mode === "setup") {
      card.innerHTML =
        '<div class="gate__brand"><span class="brand__glyph" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2.8 4.4 7v10L12 21.2 19.6 17V7z"/><path d="M12 7.6 8.2 9.8v4.4L12 16.4l3.8-2.2V9.8z"/></svg></span>' +
        "<div><strong>创建管理员账号</strong><span>首次使用，请设置登录账号</span></div></div>" +
        '<form id="gate-form" novalidate>' +
        '<div class="field"><label for="g-user">账号</label>' +
        '<input type="text" id="g-user" class="mono" autocomplete="username" placeholder="例如 admin"></div>' +
        '<div class="field"><label for="g-pass">密码</label>' +
        '<input type="password" id="g-pass" autocomplete="new-password" placeholder="至少 10 位，含字母与数字"></div>' +
        '<div class="field"><label for="g-pass2">确认密码</label>' +
        '<input type="password" id="g-pass2" autocomplete="new-password"></div>' +
        '<p class="gate__error" id="gate-error" hidden></p>' +
        '<button class="btn btn--block" type="submit">创建并登录</button>' +
        "</form>" +
        '<p class="hint gate__hint">账号只用于本机管理端登录，与客户端的授权码无关。密码可用散文式长口令，不必记随机串。</p>';
      card.querySelector("#g-user").focus();
      card.querySelector("#gate-form").addEventListener("submit", function (e) {
        e.preventDefault();
        var user = ($("#g-user").value || "").trim();
        var pass = $("#g-pass").value || "";
        var pass2 = $("#g-pass2").value || "";
        var err = $("#gate-error");
        err.hidden = true;
        if (!user) { err.textContent = "请填写账号"; err.hidden = false; return; }
        if (pass.length < 10) { err.textContent = "密码至少 10 位"; err.hidden = false; return; }
        if (pass !== pass2) { err.textContent = "两次输入的密码不一致"; err.hidden = false; return; }
        authFetch("/api/auth/setup", { username: user, password: pass }).then(function (r) {
          if (!r.ok) {
            err.textContent = (r.data && r.data.error) || "创建失败";
            err.hidden = false;
            return;
          }
          afterLogin(r.data.username || user);
        });
      });
      return;
    }

    card.innerHTML =
      '<div class="gate__brand"><span class="brand__glyph" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2.8 4.4 7v10L12 21.2 19.6 17V7z"/><path d="M12 7.6 8.2 9.8v4.4L12 16.4l3.8-2.2V9.8z"/></svg></span>' +
      "<div><strong>国研智研体 · 授权管理</strong><span>请登录后管理授权码</span></div></div>" +
      '<form id="gate-form" novalidate>' +
      '<div class="field"><label for="g-user">账号</label>' +
      '<input type="text" id="g-user" class="mono" autocomplete="username" placeholder="管理员账号"></div>' +
      '<div class="field"><label for="g-pass">密码</label>' +
      '<input type="password" id="g-pass" autocomplete="current-password" placeholder="登录密码"></div>' +
      '<p class="gate__error" id="gate-error" hidden></p>' +
      '<button class="btn btn--block" type="submit">登录</button>' +
      "</form>" +
      '<p class="hint gate__hint">连续输错 5 次会临时锁定，等待时间逐次翻倍（最长 15 分钟）。忘记密码请删除数据目录里的 licadmin.db 重新初始化（台账与私钥不受影响）。</p>';
    card.querySelector("#g-user").focus();
    card.querySelector("#gate-form").addEventListener("submit", function (e) {
      e.preventDefault();
      var user = ($("#g-user").value || "").trim();
      var pass = $("#g-pass").value || "";
      var err = $("#gate-error");
      err.hidden = true;
      if (!user || !pass) { err.textContent = "请填写账号与密码"; err.hidden = false; return; }
      authFetch("/api/auth/login", { username: user, password: pass }).then(function (r) {
        if (!r.ok) {
          err.textContent = (r.data && r.data.error) || "登录失败";
          err.hidden = false;
          return;
        }
        afterLogin(r.data.username || user);
      });
    });
  }

  // afterLogin 统一处理「登录成功后要做什么」：刷新状态、显示账号、进总览。
  function afterLogin(username) {
    state.auth.authenticated = true;
    state.auth.username = username || "";
    state.auth.method = "session";
    hideGate();
    paintAccountChip();
    loadMeta().then(function () {
      if (!/^#\//.test(location.hash)) location.hash = "#/overview";
      route();
      toast("已登录：" + (username || ""));
    });
  }

  function paintAccountChip() {
    var chip = $("#btn-account");
    var name = $("#btn-account-name");
    if (!chip || !name) return;
    if (state.auth.enabled && state.auth.authenticated) {
      chip.hidden = false;
      name.textContent = state.auth.username || "账号";
      var initial = $("#btn-account-initial");
      if (initial) initial.textContent = (state.auth.username || "A").slice(0, 1);
      chip.classList.toggle("userchip--token", state.auth.method === "token");
      chip.title = state.auth.method === "token" ? "当前是自动化令牌访问，账号管理需登录" : "账号与安全";
    } else {
      chip.hidden = true;
    }
  }

  function loadMeta() {
    return apiGet("/api/meta").then(function (meta) {
      state.meta = meta;
      return meta;
    });
  }

  function api(path, opts) {
    opts = opts || {};
    var headers = opts.headers || {};
    if (state.token) headers["X-Licadmin-Token"] = state.token;
    if (opts.body && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
    return fetch(path, {
      method: opts.method || "GET",
      headers: headers,
      body: opts.body ? JSON.stringify(opts.body) : undefined
    }).then(function (res) {
      if (res.status === 401) {
        if (state.auth.enabled) {
          showGate("login");
        } else {
          askToken("口令无效或已更换，请重新输入。");
        }
        throw new Error("unauthorized");
      }
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok) {
          var msg = (data && data.error) || ("请求失败（HTTP " + res.status + "）");
          throw new Error(msg);
        }
        return data;
      });
    });
  }
  function apiGet(path) { return api(path); }
  function apiPost(path, body) { return api(path, { method: "POST", body: body || {} }); }

  // ---------- 图标与分组导航 ----------
  //
  // 管理端的导航结构写在 JS 里而不是 index.html 里：分组折叠状态、图标、
  // 当前路由高亮都要跟着路由走，写死在 HTML 里就得在两个地方各改一遍。

  var ICONS = {
    grid: '<rect x="3.5" y="3.5" width="7" height="7" rx="1.6"/><rect x="13.5" y="3.5" width="7" height="7" rx="1.6"/><rect x="3.5" y="13.5" width="7" height="7" rx="1.6"/><rect x="13.5" y="13.5" width="7" height="7" rx="1.6"/>',
    ledger: '<path d="M5.5 3.5h13v17l-2.2-1.6-2.1 1.6-2.2-1.6-2.1 1.6-2.2-1.6z"/><path d="M9 8h6M9 12h6M9 16h3.5"/>',
    plus: '<circle cx="12" cy="12" r="8.4"/><path d="M12 8.3v7.4M8.3 12h7.4"/>',
    layers: '<path d="M12 3.4 3.6 7.9 12 12.4l8.4-4.5z"/><path d="M3.6 12.4 12 16.9l8.4-4.5"/><path d="M3.6 16.9 12 21.4l8.4-4.5"/>',
    shield: '<path d="M12 3.2 5.2 6v5.6c0 4.2 2.8 7.7 6.8 9.2 4-1.5 6.8-5 6.8-9.2V6z"/><path d="m8.9 11.9 2.2 2.2 4.3-4.4"/>',
    box: '<path d="M12 3.2 4.6 7.1v9.8L12 20.8l7.4-3.9V7.1z"/><path d="M4.6 7.1 12 11l7.4-3.9M12 11v9.8"/>',
    book: '<path d="M4.5 5.4A2.4 2.4 0 0 1 6.9 3h12.6v18H6.9a2.4 2.4 0 0 0-2.4 2.4z"/><path d="M4.5 5.4V21.4"/><path d="M8.4 8h7.6M8.4 12h7.6"/>',
    package: '<path d="M12 3.3 4.4 7.2v9.6L12 20.7l7.6-3.9V7.2z"/><path d="M4.4 7.2 12 11l7.6-3.8M12 11v9.7"/><path d="M8.2 5.2 15.8 9"/>',
    signal: '<path d="M4.4 9.4a11 11 0 0 1 15.2 0"/><path d="M7.4 12.9a7 7 0 0 1 9.2 0"/><path d="M10.3 16.3a3 3 0 0 1 3.4 0"/><circle cx="12" cy="19.5" r="1.1"/>',
    sliders: '<path d="M4 7.2h9.4M18.2 7.2H20M4 16.8h3.8M12.6 16.8H20"/><circle cx="15.8" cy="7.2" r="2.3"/><circle cx="10.4" cy="16.8" r="2.3"/>',
    user: '<circle cx="12" cy="8.4" r="3.6"/><path d="M4.9 20.2c.7-3.6 3.6-5.6 7.1-5.6s6.4 2 7.1 5.6"/>',
    database: '<ellipse cx="12" cy="6" rx="7.4" ry="3"/><path d="M4.6 6v12c0 1.7 3.3 3 7.4 3s7.4-1.3 7.4-3V6"/><path d="M4.6 12c0 1.7 3.3 3 7.4 3s7.4-1.3 7.4-3"/>',
    terminal: '<rect x="3.2" y="4.6" width="17.6" height="14.8" rx="2.6"/><path d="m7.6 9.6 2.9 2.4-2.9 2.4M12.6 15h4.2"/>',
    key: '<circle cx="8.4" cy="15.6" r="3.6"/><path d="m11 13 8-8M16.4 7.6l2 2M18.4 5.6l2 2"/>',
    chevron: '<path d="m9.5 6 6 6-6 6"/>',
    panel: '<rect x="3.4" y="4.6" width="17.2" height="14.8" rx="2.6"/><path d="M9.6 4.6v14.8"/>',
    check: '<path d="m5 12.5 4.6 4.6L19 7.6"/>',
    alert: '<path d="M12 4.4 3.2 20h17.6z"/><path d="M12 10v4.2M12 17.3v.2"/>',
    clock: '<circle cx="12" cy="12" r="8.4"/><path d="M12 7.6V12l3.1 1.9"/>',
    archive: '<rect x="3.6" y="4.4" width="16.8" height="4.2" rx="1.4"/><path d="M5.2 8.6v10.4a1.6 1.6 0 0 0 1.6 1.6h10.4a1.6 1.6 0 0 0 1.6-1.6V8.6"/><path d="M10 12.4h4"/>',
    link: '<path d="M10.4 13.6a3.6 3.6 0 0 0 5.1 0l3-3a3.6 3.6 0 0 0-5.1-5.1l-1.7 1.7"/><path d="M13.6 10.4a3.6 3.6 0 0 0-5.1 0l-3 3a3.6 3.6 0 0 0 5.1 5.1l1.7-1.7"/>'
  };

  function icon(name, cls) {
    return '<svg class="ico' + (cls ? " " + cls : "") + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" ' +
      'stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + (ICONS[name] || ICONS.grid) + "</svg>";
  }

  // 一级：独立入口（不进分组，单独排在分组之上）
  var NAV_TOP = [
    { route: "overview", label: "总览", icon: "grid" }
  ];

  // 一级分组：分组标题只承担「分区标签 + 折叠」职责，不再画图标，
  // 视觉上要比二级条目更轻、更小、更淡，否则两级看起来一模一样。
  var NAV_GROUPS = [
    {
      id: "license", label: "授权管理", items: [
        { route: "licenses", label: "授权台账", icon: "ledger" },
        // 单台与批量是同一条签发链路（后端都走 Service.Issue），
        // 差别只在「一次几台机器」和几个单台独有的字段，所以合成一页两模式。
        { route: "issue", label: "发码", icon: "plus" },
        { route: "inspect", label: "验签自检", icon: "shield" }
      ]
    },
    {
      id: "skills", label: "技能管理", items: [
        { route: "skills", label: "技能库", icon: "box" },
        { route: "skill-spec", label: "包格式与校验", icon: "book" }
      ]
    },
    {
      id: "releases", label: "版本管理", items: [
        { route: "releases", label: "发布包", icon: "package" },
        { route: "channel", label: "更新通道", icon: "signal" }
      ]
    },
    {
      id: "system", label: "系统管理", items: [
        { route: "account", label: "账号与安全", icon: "user" },
        { route: "system", label: "系统与备份", icon: "database" }
      ]
    }
  ];

  var NAV_COLLAPSE_KEY = "gy_admin_nav_groups";
  var RAIL_KEY = "gy_admin_nav_rail";

  function navCollapsed() {
    try { return JSON.parse(localStorage.getItem(NAV_COLLAPSE_KEY) || "{}") || {}; } catch (e) { return {}; }
  }
  function saveNavCollapsed(map) {
    try { localStorage.setItem(NAV_COLLAPSE_KEY, JSON.stringify(map)); } catch (e) { /* 忽略 */ }
  }

  function renderNav() {
    var nav = $("#nav");
    if (!nav) return;
    var collapsed = navCollapsed();

    function link(it) {
      return '<a class="navlink" href="#/' + it.route + '" data-nav="' + it.route + '" title="' + esc(it.label) + '">' +
        icon(it.icon) + '<span class="navlink__label">' + esc(it.label) + "</span></a>";
    }

    var top = NAV_TOP.length ? '<div class="nav-top">' + NAV_TOP.map(link).join("") + "</div>" : "";

    var groups = NAV_GROUPS.map(function (g) {
      var isOff = collapsed[g.id] === true;
      return '<div class="nav-group' + (isOff ? " is-collapsed" : "") + '" data-group="' + g.id + '">' +
        '<button class="nav-group__head" type="button" data-group-toggle="' + g.id + '" aria-expanded="' + (!isOff) + '">' +
        "<span>" + esc(g.label) + "</span>" + icon("chevron", "nav-group__chev") + "</button>" +
        '<div class="nav-group__items">' + g.items.map(link).join("") + "</div></div>";
    }).join("");

    nav.innerHTML = top + groups;

    $$("[data-group-toggle]", nav).forEach(function (btn) {
      btn.addEventListener("click", function () {
        var id = btn.getAttribute("data-group-toggle");
        var group = btn.parentNode;
        var next = !group.classList.contains("is-collapsed");
        group.classList.toggle("is-collapsed", next);
        btn.setAttribute("aria-expanded", next ? "false" : "true");
        var map = navCollapsed();
        map[id] = next;
        saveNavCollapsed(map);
      });
    });
  }

  function groupOfRoute(routeName) {
    for (var i = 0; i < NAV_TOP.length; i++) {
      if (NAV_TOP[i].route === routeName) return "";
    }
    for (var i = 0; i < NAV_GROUPS.length; i++) {
      for (var j = 0; j < NAV_GROUPS[i].items.length; j++) {
        if (NAV_GROUPS[i].items[j].route === routeName) return NAV_GROUPS[i].id;
      }
    }
    return NAV_GROUPS[0].id;
  }

  function routeLabel(routeName) {
    // #/batch 是合并前的入口，老书签仍会进来，面包屑要显示「授权管理 / 发码」。
    if (routeName === "batch") return { group: "授权管理", page: "发码" };
    for (var i = 0; i < NAV_TOP.length; i++) {
      if (NAV_TOP[i].route === routeName) return { group: "", page: NAV_TOP[i].label };
    }
    for (var i = 0; i < NAV_GROUPS.length; i++) {
      for (var j = 0; j < NAV_GROUPS[i].items.length; j++) {
        if (NAV_GROUPS[i].items[j].route === routeName) {
          return { group: NAV_GROUPS[i].label, page: NAV_GROUPS[i].items[j].label };
        }
      }
    }
    return { group: "", page: NAV_TOP.length ? NAV_TOP[0].label : "总览" };
  }

  function paintCrumb(routeName) {
    var info = routeLabel(routeName);
    var g = $("#crumb-group");
    var p = $("#crumb-page");
    if (g) {
      g.textContent = info.group;
      var sep = $("#crumb-sep");
      if (sep) sep.hidden = !info.group;
    }
    if (p) p.textContent = info.page;
  }

  function applyRail(rail) {
    $("#app").classList.toggle("rail", !!rail);
  }

  function bindRail() {
    var rail = false;
    try { rail = localStorage.getItem(RAIL_KEY) === "1"; } catch (e) { /* 忽略 */ }
    applyRail(rail);
    var btn = $("#btn-rail");
    if (btn) {
      btn.innerHTML = icon("panel");
      btn.addEventListener("click", function () {
        var next = !$("#app").classList.contains("rail");
        applyRail(next);
        btn.title = next ? "展开导航（⌘/Ctrl+B）" : "收起导航（⌘/Ctrl+B）";
        try { localStorage.setItem(RAIL_KEY, next ? "1" : "0"); } catch (e) { /* 忽略 */ }
      });
    }
    document.addEventListener("keydown", function (e) {
      if ((e.metaKey || e.ctrlKey) && (e.key === "b" || e.key === "B")) {
        e.preventDefault();
        if (btn) btn.click();
      }
    });
  }

  // ---------- 主题 ----------

  function bootTheme() {
    var forced = /[?&]theme=([ab])/.exec(location.search);
    var theme = forced ? forced[1] : "";
    if (!theme) {
      try { theme = localStorage.getItem(THEME_KEY) || ""; } catch (e) { theme = ""; }
    }
    if (theme !== "a" && theme !== "b") theme = "a";
    applyTheme(theme);
  }

  function applyTheme(theme) {
    document.documentElement.setAttribute("data-theme", theme);
    var seg = $(".seg");
    if (seg) seg.setAttribute("data-theme-slot", theme);
    $$(".seg__btn").forEach(function (btn) {
      btn.classList.toggle("is-active", btn.getAttribute("data-theme-set") === theme);
    });
  }

  function setTheme(theme, remember) {
    applyTheme(theme);
    if (remember) {
      try { localStorage.setItem(THEME_KEY, theme); } catch (e) { /* 忽略 */ }
      toast("已切换到「" + (theme === "b" ? "深空" : "浅色") + "」主题，偏好已记住");
    }
  }

  function bindTheme() {
    $$(".seg__btn").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var t = btn.getAttribute("data-theme-set");
        if (document.documentElement.getAttribute("data-theme") === t) return;
        setTheme(t, true);
      });
    });
    document.addEventListener("keydown", function (e) {
      if ((e.metaKey || e.ctrlKey) && (e.key === "j" || e.key === "J")) {
        e.preventDefault();
        setTheme(document.documentElement.getAttribute("data-theme") === "a" ? "b" : "a", true);
      }
    });
  }

  // ---------- 弹窗 ----------

  function openModal(cfg) {
    var modal = $("#modal");
    $("#modal-title").textContent = cfg.title || "";
    $("#modal-body").innerHTML = cfg.body || "";
    var foot = $("#modal-foot");
    foot.innerHTML = "";
    (cfg.actions || []).forEach(function (act) {
      var btn = document.createElement("button");
      btn.className = "btn" + (act.primary ? "" : " btn--ghost") + (act.danger ? " btn--danger" : "");
      btn.textContent = act.label;
      btn.addEventListener("click", function () {
        var keep = act.onClick ? act.onClick() : false;
        if (!keep) closeModal();
      });
      foot.appendChild(btn);
    });
    modal.hidden = false;
    if (cfg.onOpen) setTimeout(cfg.onOpen, 0);
  }

  function closeModal() {
    $("#modal").hidden = true;
    $("#modal-body").innerHTML = "";
    $("#modal-foot").innerHTML = "";
  }

  // ---------- 视图：总览 ----------

  // 总览要覆盖三条线：授权、技能、版本。三个接口互相独立，
  // 某一条取不到（例如令牌没有对应用途）不该让整页空白，所以各自兜底。
  function viewOverview() {
    var main = $("#view");
    main.innerHTML = '<div class="empty">正在加载…</div>';
    function soft(path) {
      return apiGet(path).catch(function () { return null; });
    }
    Promise.all([
      soft("/api/stats"),
      soft("/api/entries?limit=8"),
      soft("/api/skills"),
      soft("/api/releases")
    ]).then(function (out) {
      var st = out[0] || {};
      var entries = (out[1] && out[1].entries) || [];
      var skills = (out[2] && out[2].skills) || [];
      var releases = (out[3] && out[3].releases) || [];

      var pubSkills = skills.filter(function (s) { return s.status === "published"; });
      var pubReleases = releases.filter(function (r) { return r.status === "published"; });
      var latestSkill = pickNewest(skills, function (s) { return s.published_at || s.uploaded_at; });
      var latestEntry = pickNewest(entries, function (e) { return e.issued_at; });
      var latestRelease = pickNewest(pubReleases, function (r) { return r.published_at; });
      var cover = uniqueKeys(pubReleases.map(function (r) { return r.platform + "/" + r.arch; }));

      main.innerHTML = pageHead("总览", "授权签发、技能分发与版本更新的当前状态",
        '<button class="btn" id="go-issue">签发新授权码</button>') +
        '<div class="kpis kpis--3">' +
        kpi("累计签发", st.total || 0, "台账里的全部记录", "ledger") +
        kpi("当前有效", st.active || 0, "含永久授权", "shield") +
        kpi("30 天内到期", st.expiringSoon || 0, "建议提前联系客户续期", "clock") +
        kpi("已发布技能", pubSkills.length + " / " + skills.length,
          pubSkills.length ? "最新 " + latestSkill.name + " " + latestSkill.version :
            (skills.length ? "已上传 " + skills.length + " 个，尚未发布" : "还没有技能包"), "box") +
        kpi("已发布版本", pubReleases.length + " / " + releases.length,
          pubReleases.length ? "最新 " + latestRelease.version + " · " + latestRelease.platform + "/" + latestRelease.arch :
            (releases.length ? "已上传 " + releases.length + " 个，尚未发布" : "还没有发布包"), "package") +
        kpi("已作废", st.revoked || 0, "其中已过期 " + (st.expired || 0) + " 张", "archive") +
        "</div>" +

        '<div class="grid-3">' +
        '<section class="card"><div class="card__head"><h2>授权与续期</h2>' +
        '<a class="muted" href="#/licenses">授权台账 →</a></div><div class="card__body"><dl class="kv kv--rows">' +
        '<dt>当前有效</dt><dd title="' + esc((st.active || 0) + " 张有效，其中 " + (st.expiringSoon || 0) + " 张 30 天内到期") + '">' +
        (st.active || 0) + " 张 · " + (st.expiringSoon || 0) + " 张将到期</dd>" +
        '<dt>最近签发</dt><dd class="mono" title="' + esc(latestEntry ? latestEntry.serial + " · " + fmtDay(latestEntry.issued_at) : "还没有记录") + '">' +
        (latestEntry ? esc(latestEntry.serial) : "还没有记录") + "</dd>" +
        "<dt>续期规则</dt><dd>新签一张，旧码作废</dd>" +
        "</dl></div></section>" +

        '<section class="card"><div class="card__head"><h2>技能分发</h2>' +
        '<a class="muted" href="#/skills">技能库 →</a></div><div class="card__body"><dl class="kv kv--rows">' +
        "<dt>已发布</dt><dd>" + pubSkills.length + " / " + skills.length + " 个</dd>" +
        '<dt>最近发布</dt><dd title="' + esc(latestSkill ? latestSkill.name + " " + latestSkill.version : "还没有技能") + '">' +
        (latestSkill ? esc(latestSkill.name) + " " + esc(latestSkill.version) : "还没有技能") + "</dd>" +
        '<dt>进包方式</dt><dd><code>~/.weknora/skills</code></dd>' +
        "</dl></div></section>" +

        '<section class="card"><div class="card__head"><h2>版本与更新</h2>' +
        '<a class="muted" href="#/releases">发布包 →</a></div><div class="card__body"><dl class="kv kv--rows">' +
        "<dt>已发布</dt><dd>" + pubReleases.length + " / " + releases.length + " 个 · " + cover.length + " 个平台</dd>" +
        '<dt>最新版本</dt><dd title="' + esc(latestRelease ? latestRelease.version + " · " + latestRelease.channel + " · " + fmtDay(latestRelease.published_at) : "还没有已发布版本") + '">' +
        (latestRelease ? esc(latestRelease.version) + " · " + esc(latestRelease.channel) : "还没有已发布版本") + "</dd>" +
        '<dt>更新接口</dt><dd><code>GET /api/updates/latest</code></dd>' +
        "</dl></div></section>" +
        "</div>" +

        '<div class="grid-2--fill">' +
        '<section class="card"><div class="card__head"><h2>最近动态</h2>' +
        '<span class="muted">签发与发布的最新动作</span></div><div class="card__body">' +
        feedList(buildFeed(entries, skills, releases)) + "</div></section>" +
        '<section class="card"><div class="card__head"><h2>快捷操作</h2>' +
        '<span class="muted">常用动作一步直达</span></div><div class="card__body"><div class="quick-list">' +
        quick("#/issue", "plus", "签发授权码", "单台机器，填指纹即可") +
        quick("#/skills", "box", "导入技能", "上传 zip 并发布") +
        quick("#/releases", "package", "上传发布包", "供客户端检查更新") +
        quick("#/system", "database", "备份数据", "导出或恢复整站数据") +
        "</div></div></section>" +
        "</div>";

      $("#go-issue").addEventListener("click", function () { location.hash = "#/issue"; });
    }).catch(function (err) {
      if (err.message !== "unauthorized") main.innerHTML = errorBlock(err.message);
    });
  }

  function pickNewest(list, key) {
    var best = null, bestTs = -1;
    (list || []).forEach(function (it) {
      var ts = key(it) || 0;
      if (ts > bestTs) { bestTs = ts; best = it; }
    });
    return best;
  }

  function uniqueKeys(list) {
    var seen = {}, out = [];
    (list || []).forEach(function (v) {
      if (v && !seen[v]) { seen[v] = 1; out.push(v); }
    });
    return out;
  }

  // 签发与发布混在一条时间线里：运营关心的是「最近做了什么」，
  // 而不是「哪个模块做了什么」，所以合并排序而不是三张表并排。
  function buildFeed(entries, skills, releases) {
    var items = [];
    entries.forEach(function (e) {
      items.push({
        ts: e.issued_at || 0, cls: "ok",
        t: "签发授权码 " + e.serial,
        s: (e.user || "—") + " · " + (e.org || "—")
      });
    });
    skills.forEach(function (s) {
      var pub = s.status === "published";
      items.push({
        ts: s.published_at || s.uploaded_at || 0, cls: pub ? "ok" : "",
        t: (pub ? "发布技能 " : "上传技能 ") + s.name + " " + s.version,
        s: pub ? "已进包，客户端下次打包会带上" : "草稿，还没发布"
      });
    });
    releases.forEach(function (r) {
      var pub = r.status === "published";
      items.push({
        ts: r.published_at || r.uploaded_at || 0, cls: pub ? "ok" : "",
        t: (pub ? "发布版本 " : "上传安装包 ") + r.version,
        s: r.platform + "/" + r.arch + " · " + r.channel + (pub ? "" : " · 草稿")
      });
    });
    items.sort(function (a, b) { return b.ts - a.ts; });
    return items.slice(0, 6);
  }

  function feedList(items) {
    if (!items.length) {
      return '<div class="empty"><strong>还没有动态</strong><span>签发授权码或发布内容后会出现在这里</span></div>';
    }
    return '<div class="feed">' + items.map(function (it) {
      return '<div class="feed__row"><span class="feed__dot' + (it.cls ? " feed__dot--" + it.cls : "") + '"></span>' +
        '<div class="feed__main"><div class="feed__t">' + esc(it.t) + "</div>" +
        '<div class="feed__s">' + esc(it.s) + "</div></div>" +
        '<span class="feed__time mono">' + esc(fmtTime(it.ts)) + "</span></div>";
    }).join("") + "</div>";
  }

  function pageHead(title, sub, actions) {
    return '<div class="view-head"><div><h1>' + esc(title) + "</h1><p>" + (sub || "") + "</p></div>" +
      (actions ? '<div class="row">' + actions + "</div>" : "") + "</div>";
  }

  function quick(href, ico, title, sub) {
    return '<a class="quick" href="' + href + '"><span class="quick__ico">' + icon(ico) + "</span>" +
      "<div><div class=\"quick__t\">" + esc(title) + '</div><div class="quick__s">' + esc(sub) + "</div></div></a>";
  }

  function kpi(label, value, meta, ico) {
    return '<div class="card kpi">' + (ico ? '<span class="kpi__ico">' + icon(ico) + "</span>" : "") +
      '<div class="kpi__label">' + esc(label) + "</div>" +
      '<div class="kpi__value">' + esc(value) + "</div>" +
      '<div class="kpi__meta">' + esc(meta) + "</div></div>";
  }

  function recentTable(rows) {
    if (!rows.length) return '<div class="empty"><strong>还没有签发记录</strong><span>从「单条发码」开始</span></div>';
    return '<div class="table-wrap"><table class="table"><thead><tr>' +
      "<th>序列号</th><th>使用人 / 单位</th><th>到期</th><th>状态</th>" +
      "</tr></thead><tbody>" + rows.map(function (e) {
        return "<tr>" +
          '<td class="mono">' + esc(e.serial) + "</td>" +
          "<td>" + esc(e.user || "—") + '<span class="muted"> · ' + esc(e.org || "—") + "</span></td>" +
          '<td class="nowrap">' + esc(fmtDay(e.expires_at)) + "</td>" +
          "<td>" + statusPill(e) + "</td>" +
          "</tr>";
      }).join("") + "</tbody></table></div>";
  }

  function statusPill(e) {
    if (e.status === "revoked") return '<span class="pill pill--off"><i></i>已作废</span>';
    var left = daysLeft(e.expires_at);
    if (left < 0) return '<span class="pill pill--danger"><i></i>已过期</span>';
    if (left <= 30) return '<span class="pill pill--warn" title="剩余 ' + left + ' 天"><i></i>' + left + " 天</span>";
    if (e.parent_id) return '<span class="pill pill--ok"><i></i>有效 · 续期</span>';
    return '<span class="pill pill--ok"><i></i>有效</span>';
  }

  function errorBlock(msg) {
    return '<div class="empty"><strong>没能取到数据</strong><span>' + esc(msg) + "</span>" +
      '<button class="btn btn--ghost" onclick="location.reload()">重试</button></div>';
  }

  // ---------- 视图：台账 ----------

  function viewLicenses() {
    var main = $("#view");
    main.innerHTML =
      '<div class="view-head"><div><h1>授权台账</h1><p>按序列号、使用人、单位、机器指纹检索</p></div>' +
      '<div class="row"><button class="btn btn--ghost" id="btn-export">导出台账 CSV</button>' +
      '<button class="btn" id="go-issue2">签发新授权码</button></div></div>' +
      '<section class="card"><div class="card__head">' +
      '<input type="text" id="kw" placeholder="搜索序列号 / 使用人 / 单位 / 指纹" style="max-width:320px">' +
      '<select id="status-filter" style="width:130px">' +
      '<option value="">全部状态</option><option value="active">仅有效</option><option value="revoked">仅已作废</option>' +
      "</select></div>" +
      '<div id="license-table"><div class="empty">正在加载…</div></div>' +
      '<div class="pager" id="pager"></div></section>';

    $("#kw").value = state.licenses.keyword;
    $("#status-filter").value = state.licenses.status;
    $("#go-issue2").addEventListener("click", function () { location.hash = "#/issue"; });
    $("#btn-export").addEventListener("click", exportCsv);

    var timer = null;
    $("#kw").addEventListener("input", function () {
      clearTimeout(timer);
      var v = $("#kw").value;
      timer = setTimeout(function () {
        state.licenses.keyword = v;
        state.licenses.offset = 0;
        loadLicenses();
      }, 250);
    });
    $("#status-filter").addEventListener("change", function () {
      state.licenses.status = $("#status-filter").value;
      state.licenses.offset = 0;
      loadLicenses();
    });
    loadLicenses();
  }

  // exportCsv 用带口令头的请求取 CSV，再触发浏览器下载。
  //
  // 不能用 location.href 直接把 t 拼在地址上：口令会留在浏览器历史与
  // 服务器日志里，而且导出接口只认请求头（不认 query）。
  function exportCsv() {
    var headers = {};
    if (state.token) headers["X-Licadmin-Token"] = state.token;
    fetch("/api/export", { headers: headers }).then(function (res) {
      if (res.status === 401) { askToken("口令无效或已更换，请重新输入。"); return null; }
      if (!res.ok) throw new Error("导出失败（HTTP " + res.status + "）");
      return res.blob();
    }).then(function (blob) {
      if (!blob) return;
      var url = URL.createObjectURL(blob);
      var a = document.createElement("a");
      a.href = url;
      a.download = "授权台账-" + new Date().toISOString().slice(0, 10) + ".csv";
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
      toast("台账已导出");
    }).catch(function (err) { toast(err.message); });
  }

  function loadLicenses() {
    var box = $("#license-table");
    if (!box) return;
    box.innerHTML = '<div class="empty">正在加载…</div>';
    var q = "?limit=" + PAGE_SIZE + "&offset=" + state.licenses.offset;
    if (state.licenses.keyword) q += "&keyword=" + encodeURIComponent(state.licenses.keyword);
    if (state.licenses.status) q += "&status=" + encodeURIComponent(state.licenses.status);
    apiGet("/api/entries" + q).then(function (data) {
      state.licenses.rows = data.entries || [];
      state.licenses.total = data.total || 0;
      renderLicenseTable();
    }).catch(function (err) {
      if (err.message !== "unauthorized") box.innerHTML = errorBlock(err.message);
    });
  }

  function renderLicenseTable() {
    var box = $("#license-table");
    var rows = state.licenses.rows;
    if (!rows.length) {
      box.innerHTML = '<div class="empty"><strong>没有匹配的记录</strong><span>换个关键词或清空筛选试试</span></div>';
      $("#pager").innerHTML = "";
      return;
    }
    box.innerHTML = '<div class="table-wrap"><table class="table"><thead><tr>' +
      "<th>序列号</th><th>使用人 / 单位</th><th>功能</th><th>机器指纹</th><th>到期</th><th>状态</th><th></th>" +
      "</tr></thead><tbody>" + rows.map(function (e) {
        return '<tr data-id="' + e.id + '">' +
          '<td class="mono">' + esc(e.serial) + "</td>" +
          "<td>" + esc(e.user || "—") + '<div class="muted">' + esc(e.org || "—") + "</div></td>" +
          '<td class="muted nowrap">' + esc(featureText(e.features)) + "</td>" +
          '<td class="mono muted" title="' + esc(e.fingerprint) + '">' + esc(shortFp(e.fingerprint)) + "</td>" +
          '<td class="nowrap">' + esc(fmtDay(e.expires_at)) + "</td>" +
          "<td>" + statusPill(e) + "</td>" +
          '<td class="nowrap"><button class="btn btn--sm btn--ghost" data-detail="' + e.id + '">详情</button></td>' +
          "</tr>";
      }).join("") + "</tbody></table></div>";
    renderPager();
    bindRowActions(box);
  }

  // firstField 取一行 CSV 的首个字段，用于在结果表里回显 MAC。
  function firstField(raw) {
    if (!raw) return "";
    var m = /^[^,\t]*/.exec(String(raw).trim());
    return m ? m[0] : "";
  }

  function shortFp(fp) {
    if (!fp) return "—";
    return fp.slice(0, 10) + "…";
  }

  function renderPager() {
    var total = state.licenses.total;
    var from = total ? state.licenses.offset + 1 : 0;
    var to = Math.min(state.licenses.offset + PAGE_SIZE, total);
    var pager = $("#pager");
    if (!pager) return;
    pager.innerHTML =
      "<span>共 " + total + " 条" + (total ? "，当前 " + from + "–" + to : "") + "</span>" +
      '<span class="row">' +
      '<button class="btn btn--sm btn--ghost" id="prev-page"' + (state.licenses.offset <= 0 ? " disabled" : "") + ">上一页</button>" +
      '<button class="btn btn--sm btn--ghost" id="next-page"' + (to >= total ? " disabled" : "") + ">下一页</button>" +
      "</span>";
    $("#prev-page").addEventListener("click", function () {
      state.licenses.offset = Math.max(0, state.licenses.offset - PAGE_SIZE);
      loadLicenses();
    });
    $("#next-page").addEventListener("click", function () {
      state.licenses.offset += PAGE_SIZE;
      loadLicenses();
    });
  }

  function bindRowActions(root) {
    $$("[data-detail]", root).forEach(function (btn) {
      btn.addEventListener("click", function () {
        openDetail(parseInt(btn.getAttribute("data-detail"), 10));
      });
    });
  }

  function openDetail(id) {
    apiGet("/api/entries/" + id).then(function (e) {
      var parent = e.parent_id
        ? '<a href="#" data-open="' + e.parent_id + '">#' + e.parent_id + "</a>"
        : "—";
      openModal({
        title: "授权详情 · " + e.serial,
        body:
          '<dl class="kv">' +
          "<dt>状态</dt><dd>" + statusPill(e) + "</dd>" +
          "<dt>使用人</dt><dd>" + esc(e.user || "—") + "</dd>" +
          "<dt>单位</dt><dd>" + esc(e.org || "—") + "</dd>" +
          "<dt>功能</dt><dd>" + esc(featureText(e.features)) + "</dd>" +
          "<dt>签发</dt><dd>" + esc(fmtTime(e.issued_at)) + "</dd>" +
          "<dt>到期</dt><dd>" + esc(fmtTime(e.expires_at)) + "</dd>" +
          "<dt>机器指纹</dt><dd class=\"mono\">" + esc(e.fingerprint || "—") + "</dd>" +
          "<dt>网卡</dt><dd class=\"mono\">" + esc((e.macs || []).join("\n") || "—").replace(/\n/g, "<br>") + "</dd>" +
          "<dt>续期自</dt><dd>" + parent + "</dd>" +
          // 签发者指纹：排查「客户端不认这张码」时，先比客户端授权页上的
          // 本机 key_id 与本行是否一致，一眼就能分出「码坏了」还是「换过钥」。
          (e.key_id ? "<dt>签发密钥</dt><dd class=\"mono\">" + esc(e.key_id) + "</dd>" : "") +
          (e.revoke_reason ? "<dt>作废原因</dt><dd>" + esc(e.revoke_reason) + "</dd>" : "") +
          (e.note ? "<dt>备注</dt><dd>" + esc(e.note) + "</dd>" : "") +
          "</dl><div class=\"divider\"></div>" +
          '<div class="row"><span class="eyebrow">授权码</span><span class="spacer"></span>' +
          '<button class="btn btn--sm btn--ghost" id="copy-code">复制</button></div>' +
          '<div class="code code--lg" id="code-box">' + esc(e.code) + "</div>",
        actions: [
          { label: "作废", danger: true, onClick: function () { openRevoke(e); return false; } },
          { label: "续期", onClick: function () { openRenew(e); return false; } },
          { label: "关闭", primary: true }
        ],
        onOpen: function () {
          $("#copy-code").addEventListener("click", function () { copyText(e.code, "授权码"); });
          $$("[data-open]").forEach(function (a) {
            a.addEventListener("click", function (ev) {
              ev.preventDefault();
              openDetail(parseInt(a.getAttribute("data-open"), 10));
            });
          });
        }
      });
    }).catch(function (err) {
      if (err.message !== "unauthorized") toast(err.message);
    });
  }

  function openRenew(e) {
    openModal({
      title: "续期 · " + e.serial,
      body:
        '<p class="hint">续期会新签一张码并把当前这张作废，客户用新码覆盖即可。' +
        "未到期时从原到期日顺延，已过期则从今天算起。</p>" +
        '<div class="row" style="margin:12px 0">' +
        '<div class="field" style="flex:1;margin:0"><label>续期天数</label>' +
        '<input type="number" id="renew-days" value="365" min="1" step="1"></div>' +
        '<div class="field" style="flex:1;margin:0"><label>当前到期</label>' +
        '<input type="text" value="' + esc(fmtDay(e.expires_at)) + '" disabled></div>' +
        "</div>" +
        '<div class="row" style="margin-bottom:12px">' +
        ["90", "180", "365", "730"].map(function (d) {
          return '<button class="chip" data-days="' + d + '">' + d + " 天</button>";
        }).join("") + "</div>" +
        '<div class="field"><label>备注（写进台账）</label>' +
        '<input type="text" id="renew-note" placeholder="留空则自动写「N 天续期」"></div>' +
        '<p class="hint" id="renew-preview"></p>',
      actions: [
        { label: "确认续期", primary: true, onClick: function () {
          var days = parseInt(($("#renew-days").value || "0"), 10);
          if (!days || days <= 0) { toast("续期天数必须大于 0"); return true; }
          apiPost("/api/entries/" + e.id + "/renew", { days: days, note: $("#renew-note").value })
            .then(function (res) { showIssued(res, "续期完成"); })
            .catch(function (err) { toast(err.message); });
          return false;
        } }
      ],
      onOpen: function () {
        var daysEl = $("#renew-days");
        var preview = function () {
          var d = parseInt(daysEl.value || "0", 10) || 0;
          var base = Math.max(Date.now(), (e.expires_at || 0) * 1000);
          var next = new Date(base + d * 86400000);
          $("#renew-preview").textContent = d > 0
            ? "新码到期：" + next.getFullYear() + "-" + pad(next.getMonth() + 1) + "-" + pad(next.getDate())
            : "";
        };
        daysEl.addEventListener("input", preview);
        $$("[data-days]").forEach(function (c) {
          c.addEventListener("click", function () { daysEl.value = c.getAttribute("data-days"); preview(); });
        });
        preview();
      }
    });
  }

  function openRevoke(e) {
    openModal({
      title: "作废 · " + e.serial,
      body:
        '<p class="hint">作废后客户端下一次校验即失效，且不可撤销。</p>' +
        '<div class="field" style="margin-top:12px"><label>作废原因</label>' +
        '<select id="revoke-reason"><option>换机重签</option><option>客户退订</option>' +
        "<option>信息填错</option><option>测试数据</option><option>其它</option></select></div>" +
        '<div class="field"><label>备注</label><input type="text" id="revoke-note" placeholder="选填，比如工单号"></div>',
      actions: [
        { label: "确认作废", primary: true, danger: true, onClick: function () {
          apiPost("/api/entries/" + e.id + "/revoke", {
            reason: $("#revoke-reason").value,
            note: $("#revoke-note").value
          }).then(function () {
            toast("已作废");
            route();
          }).catch(function (err) { toast(err.message); });
          return false;
        } }
      ]
    });
  }

  // ---------- 视图：单条发码 ----------

  // ---------- 视图：发码（单台 / 批量粘贴 两模式） ----------
  //
  // 合并理由：后端 /api/issue 与 /api/issue/batch 最终都调用同一个
  // Service.Issue，签出来的码格式、台账记录、校验方式完全一致；差别只有
  // 「一次几台机器」和几个单台独有字段（使用人、序列号、备注、强制签发）。
  // 拆成两页时这几十行表单要维护两遍，参数一改就漏。
  function viewIssue() {
    var main = $("#view");
    var batchMode = currentRoute() === "batch";
    main.innerHTML =
      '<div class="view-head"><div><h1>发码</h1>' +
      '<p>一台机器一张码；多台机器用「批量粘贴」一次签完</p></div></div>' +
      '<div class="seg" id="issue-mode">' +
      '<button class="seg__btn" type="button" data-mode="single">单台</button>' +
      '<button class="seg__btn" type="button" data-mode="batch">批量粘贴</button>' +
      "</div>" +
      '<div class="grid-2" id="issue-pane">' +
      '<section class="card"><div class="card__body">' +

      // ---- 数据来源：两种模式各一个输入区 ----
      '<div class="field" data-only="single"><label for="f-macs">MAC 地址（一行一个，支持 ： - 或 12 位连写）</label>' +
      '<textarea id="f-macs" class="mono" placeholder="fc:b2:14:ba:44:f0"></textarea>' +
      '<span class="hint">一台机器的全部物理网卡更稳；换 USB 网卡不影响已签发的码。</span></div>' +

      '<div class="field" data-only="batch"><label for="b-text">批量数据（一行一台机器）</label>' +
      '<textarea id="b-text" class="mono code-input" placeholder="fc:b2:14:ba:44:f0,张三,某某研究院,365,kb+chat&#10;fc:b2:14:ba:44:f1,李四,某某研究院,730"></textarea>' +
      '<span class="hint">每行：MAC[,使用人,单位,天数,功能]。功能用 + 连接（kb+chat+ocr+office），留空等于全开。' +
      "MAC 列可用分号并列多块网卡。</span></div>" +

      // ---- 公共参数 ----
      // 单台独有字段统一放第一行；第二行两个字段两种模式都在，
      // 这样批量模式下不会出现「一列全宽、下面又一列全宽」的空旷感。
      '<div class="row" data-only="single"><div class="field grow"><label for="f-user">使用人</label>' +
      '<input type="text" id="f-user" placeholder="张三"></div>' +
      '<div class="field grow"><label for="f-serial">序列号（留空自动生成）</label>' +
      '<input type="text" id="f-serial" class="mono" placeholder="SN-20261123-BA44"></div></div>' +

      '<div class="row"><div class="field grow"><label for="f-org" id="lbl-org">单位</label>' +
      '<input type="text" id="f-org" placeholder="某某研究院"></div>' +
      '<div class="field grow"><label for="f-days" id="lbl-days">有效天数（0 = 永久）</label>' +
      '<input type="number" id="f-days" value="365" min="0" step="1"></div></div>' +

      '<div class="field"><label>功能位</label><div class="chips" id="f-features">' +
      FEATURES.map(function (f) {
        return '<label class="chip is-on" data-feature="' + f.key + '">' +
          '<input type="checkbox" checked>' + esc(f.label) + "</label>";
      }).join("") + "</div>" +
      '<span class="hint">不勾选任何项等于全开。</span></div>' +

      '<div class="field" data-only="single"><label for="f-note">备注</label>' +
      '<input type="text" id="f-note" placeholder="选填，写进台账"></div>' +

      '<label class="chip chip--line" data-only="single">' +
      '<input type="checkbox" id="f-force"> 强制签发（同机已有有效码时仍签发）</label>' +

      // 复位码是「解死锁」专用开关：它会清空客户端记录的历史到期水位。
      // 收进 details 里，正常发码时根本看不到，减少误勾的概率。
      '<details class="advanced" data-only="single" style="margin-top:8px">' +
      '<summary class="hint" style="cursor:pointer">高级：复位码</summary>' +
      '<label class="chip chip--line" style="margin-top:8px">' +
      '<input type="checkbox" id="f-reset"> 签发复位码（客户端导入时清空历史到期水位）</label>' +
      '<p class="hint">只在客户「导入时报『到期早于已用过的授权』」且确实要用一张更早到期的码救他时才勾。' +
      '勾选后客户端会丢弃历史水位，等于放弃那台机器的回滚防护，动作会在客户端审计里留痕。</p>' +
      "</details>" +

      '<div class="divider"></div>' +
      '<div class="row"><button class="btn" id="btn-issue" data-only="single">签发</button>' +
      '<button class="btn" id="btn-batch" data-only="batch">开始签发</button>' +
      '<button class="btn btn--ghost" id="btn-issue-clear" data-only="single">清空</button>' +
      '<span class="hint" id="issue-hint">⌘/Ctrl + Enter 提交</span></div>' +

      "</div></section>" +

      // ---- 右侧：说明 / 结果 ----
      '<section class="card"><div class="card__head"><h2 id="issue-side-title">签发说明</h2></div>' +
      '<div class="card__body" id="issue-side-single">' +
      '<dl class="kv">' +
      "<dt>绑定粒度</dt><dd>绑定到「机器指纹」，任一网卡匹配即通过，所以加一块网卡不会让旧码失效。</dd>" +
      "<dt>同机多码</dt><dd>默认拒绝；确实要签第二张时勾「强制签发」，便于换机或码损坏。</dd>" +
      "<dt>有效期</dt><dd>天数为 0 表示永久；永久码没有续期概念。</dd>" +
      "<dt>批量差异</dt><dd>批量签发的码与单台完全一致，只是不能指定序列号与备注，且不会强制覆盖已有码。</dd>" +
      '<dt>公钥</dt><dd id="pubkey-mini" class="mono muted">—</dd>' +
      "</dl></div>" +
      '<div class="card__body" id="issue-side-batch" hidden>' +
      '<div class="empty"><strong>还没有执行</strong><span>粘贴数据后点「开始签发」</span></div></div>' +
      "</section></div>";

    function setMode(mode) {
      var pane = $("#issue-pane");
      pane.setAttribute("data-mode", mode);
      $$("#issue-mode .seg__btn").forEach(function (b) {
        b.classList.toggle("is-active", b.getAttribute("data-mode") === mode);
      });
      $("#lbl-org").textContent = mode === "batch" ? "默认单位" : "单位";
      $("#lbl-days").textContent = mode === "batch" ? "默认天数（0 = 永久）" : "有效天数（0 = 永久）";
      $("#issue-side-title").textContent = mode === "batch" ? "结果" : "签发说明";
      $("#issue-side-single").hidden = mode === "batch";
      $("#issue-side-batch").hidden = mode !== "batch";
      $("#issue-hint").textContent = mode === "batch"
        ? "已存在的有效码会被跳过并标红，避免重复发码"
        : "⌘/Ctrl + Enter 提交";
      state.issueMode = mode;
    }

    $$("#issue-mode .seg__btn").forEach(function (btn) {
      btn.addEventListener("click", function () { setMode(btn.getAttribute("data-mode")); });
    });

    $$("#f-features .chip").forEach(function (chip) {
      chip.addEventListener("click", function () {
        var input = chip.querySelector("input");
        setTimeout(function () { chip.classList.toggle("is-on", input.checked); }, 0);
      });
    });

    $("#btn-issue-clear").addEventListener("click", function () {
      ["f-macs", "f-user", "f-org", "f-serial", "f-note"].forEach(function (id) {
        var el = $("#" + id); if (el) el.value = "";
      });
      $("#f-days").value = "365";
    });
    $("#btn-issue").addEventListener("click", submitIssue);
    $("#btn-batch").addEventListener("click", submitBatch);

    setMode(batchMode ? "batch" : "single");

    if (state.meta && state.meta.public_key) {
      var mini = $("#pubkey-mini");
      // 指纹比 base64 好核对：客户端授权页显示同一个 KID，两边一对即知同不同源。
      var kid = (state.meta && state.meta.key_id) || "";
      if (mini) mini.textContent = kid ? ("KID " + kid) : (state.meta.public_key.slice(0, 16) + "…");
    }
  }

  function submitBatch() {
    var text = $("#b-text").value;
    if (!text.trim()) { toast("请先粘贴要签发的数据"); return; }
    var btn = $("#btn-batch");
    btn.disabled = true;
    apiPost("/api/issue/batch", {
      text: text,
      days: parseInt($("#f-days").value || "0", 10),
      org: $("#f-org").value,
      features: selectedFeatures()
    }).then(function (res) {
      renderBatchResult(res);
    }).catch(function (err) {
      if (err.message !== "unauthorized") toast(err.message);
    }).then(function () { btn.disabled = false; });
  }

  function issueHotkey(e) {
    if (!/^#\/(issue|batch)/.test(location.hash)) return;
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      // 两种模式共用快捷键，提交目标跟着当前模式走。
      if (state.issueMode === "batch") submitBatch(); else submitIssue();
    }
  }

  function selectedFeatures() {
    return $$("#f-features .chip").filter(function (c) {
      return c.querySelector("input").checked;
    }).map(function (c) { return c.getAttribute("data-feature"); });
  }

  function submitIssue() {
    var macs = $("#f-macs").value.split(/[\s,;]+/).map(function (s) { return s.trim(); }).filter(Boolean);
    if (!macs.length) { toast("请先填入 MAC 地址"); return; }
    var days = parseInt($("#f-days").value || "0", 10);
    if (isNaN(days) || days < 0) { toast("有效天数不能为负"); return; }
    var body = {
      macs: macs,
      user: $("#f-user").value,
      org: $("#f-org").value,
      serial: $("#f-serial").value,
      days: days,
      features: selectedFeatures(),
      note: $("#f-note").value,
      force: $("#f-force").checked,
      reset: !!($("#f-reset") && $("#f-reset").checked)
    };
    var btn = $("#btn-issue");
    btn.disabled = true;
    apiPost("/api/issue", body).then(function (res) {
      showIssued(res, "签发成功");
    }).catch(function (err) {
      if (err.message !== "unauthorized") toast(err.message);
    }).then(function () { btn.disabled = false; });
  }

  function showIssued(res, title) {
    var e = res.entry || res.Entry || res;
    var code = e.code || res.code || "";
    openModal({
      title: title,
      body:
        (res.warning ? '<p class="hint" style="color:var(--warn)">' + esc(res.warning) + "</p>" : "") +
        '<dl class="kv"><dt>序列号</dt><dd class="mono">' + esc(e.serial) + "</dd>" +
        "<dt>使用人</dt><dd>" + esc(e.user || "—") + "</dd>" +
        "<dt>到期</dt><dd>" + esc(fmtTime(e.expires_at)) + "</dd>" +
        "<dt>功能</dt><dd>" + esc(featureText(e.features)) + "</dd></dl>" +
        '<div class="divider"></div>' +
        '<div class="row"><span class="eyebrow">授权码</span><span class="spacer"></span>' +
        '<button class="btn btn--sm btn--ghost" id="copy-code2">复制</button></div>' +
        '<div class="code code--lg">' + esc(code) + "</div>" +
        '<p class="hint">把这段码发给用户，在入口页粘贴即可进入系统。</p>',
      actions: [{ label: "完成", primary: true }],
      onOpen: function () {
        $("#copy-code2").addEventListener("click", function () { copyText(code, "授权码"); });
      }
    });
    route();
  }

  function renderBatchResult(res) {
    var rows = res.rows || res.Rows || [];
    var box = $("#issue-side-batch");
    if (!rows.length) {
      box.innerHTML = '<div class="empty"><strong>没有产生记录</strong><span>检查一下粘贴格式</span></div>';
      return;
    }
    var ok = rows.filter(function (r) { return r.entry; }).length;
    box.innerHTML =
      '<p class="hint">共 ' + rows.length + " 行，成功 " + ok + " 行。</p>" +
      '<div class="table-wrap"><table class="table"><thead><tr><th>行</th><th>MAC</th><th>结果</th></tr></thead><tbody>' +
      rows.map(function (r) {
        var e = r.entry;
        var err = r.err;
        return "<tr><td class=\"num\">" + esc(r.index) + "</td>" +
          '<td class="mono">' + esc(firstField(r.raw)) + "</td>" +
          "<td>" + (e
            ? '<span class="pill pill--ok"><i></i>' + esc(e.serial) + "</span>"
            : '<span class="pill pill--danger"><i></i>' + esc(err || "失败") + "</span>") + "</td></tr>";
      }).join("") + "</tbody></table></div>" +
      (ok ? '<div class="row" style="margin-top:12px"><button class="btn btn--ghost" id="copy-batch">复制成功的码</button></div>' : "");

    var copyBtn = $("#copy-batch");
    if (copyBtn) {
      copyBtn.addEventListener("click", function () {
        var codes = rows.filter(function (r) { return r.entry; })
          .map(function (r) { return r.entry.code; });
        copyText(codes.join("\n"), "授权码");
      });
    }
  }

  // ---------- 视图：验签 ----------

  function viewInspect() {
    var main = $("#view");
    main.innerHTML =
      '<div class="view-head"><div><h1>验签自检</h1><p>客户报「授权码无效」时，先在这里验一遍</p></div></div>' +
      '<div class="grid-2"><section class="card"><div class="card__body">' +
      '<div class="field"><label>授权码</label>' +
      '<textarea id="i-code" class="mono" style="min-height:150px" placeholder="WNL1...."></textarea></div>' +
      '<div class="row"><button class="btn" id="btn-inspect">验签</button>' +
      '<span class="hint">只读操作，不会改动台账</span></div>' +
      "</div></section>" +
      '<section class="card"><div class="card__head"><h2>结果</h2></div><div class="card__body" id="inspect-result">' +
      '<div class="empty"><strong>等待验签</strong><span>粘贴授权码后点「验签」</span></div>' +
      "</div></section></div>";

    $("#btn-inspect").addEventListener("click", function () {
      var code = $("#i-code").value;
      if (!code.trim()) { toast("请先粘贴授权码"); return; }
      apiPost("/api/inspect", { code: code }).then(function (r) {
        var box = $("#inspect-result");
        if (!r.valid) {
          box.innerHTML = '<div class="empty"><strong>验签失败</strong><span>' + esc(r.error || "签名或格式不正确") + "</span></div>";
          return;
        }
        box.innerHTML =
          '<dl class="kv">' +
          '<dt>结论</dt><dd><span class="pill pill--ok"><i></i>签名有效</span> ' +
          (r.ledger_id ? '<span class="pill"><i></i>台账 #' + r.ledger_id + "</span>" : '<span class="pill pill--warn"><i></i>台账中未找到</span>') + "</dd>" +
          "<dt>序列号</dt><dd class=\"mono\">" + esc(r.serial) + "</dd>" +
          "<dt>使用人</dt><dd>" + esc(r.user || "—") + "</dd>" +
          "<dt>单位</dt><dd>" + esc(r.org || "—") + "</dd>" +
          "<dt>签发</dt><dd>" + esc(fmtTime(r.issued_at)) + "</dd>" +
          "<dt>到期</dt><dd>" + esc(fmtTime(r.expires_at)) + "</dd>" +
          "<dt>功能</dt><dd>" + esc(featureText(r.features)) + "</dd>" +
          "<dt>机器指纹</dt><dd class=\"mono\">" + esc(r.fingerprint) + "</dd>" +
          "<dt>网卡</dt><dd class=\"mono\">" + esc((r.macs || []).join(" / ") || "—") + "</dd>" +
          "</dl>";
      }).catch(function (err) { if (err.message !== "unauthorized") toast(err.message); });
    });
  }

  // ---------- 视图：设置 ----------

  // ---------- 视图：系统（备份 / 恢复 / 令牌 / 日志） ----------

  function viewSystem() {
    var main = $("#view");
    main.innerHTML =
      '<div class="view-head"><div><h1>系统</h1><p>数据备份与恢复、访问令牌、运行日志</p></div>' +
      '<div class="row"><button class="btn btn--ghost" id="btn-refresh-system">刷新</button></div></div>' +
      '<div class="grid-2">' +
      '<section class="card"><div class="card__head"><h2>数据位置</h2></div>' +
      '<div class="card__body" id="system-files"><div class="empty"><strong>加载中</strong><span>读取数据目录</span></div></div></section>' +
      '<section class="card"><div class="card__head"><h2>备份与恢复</h2></div><div class="card__body">' +
      '<p class="hint">备份包含账号库、授权台账、密钥与技能包（发行包按需勾选）。恢复是「重启后生效」：' +
      '上传后会先暂存，管理端下次启动时才应用，并自动把原数据挪到 <span class="mono">backup-before-restore-&lt;时间&gt;/</span>。</p>' +
      '<div class="row" style="margin-top:10px">' +
      '<button class="btn" id="btn-backup">下载备份</button>' +
      '<label class="check"><input type="checkbox" id="backup-releases"> <span>包含发行包（体积大）</span></label>' +
      '<button class="btn btn--ghost" id="btn-restore">上传恢复包…</button>' +
      '<input type="file" id="restore-file" accept=".zip" hidden>' +
      "</div>" +
      '<div id="restore-note"></div>' +
      "</div></section>" +
      "</div>" +
      '<section class="card" style="margin-top:16px"><div class="card__head"><h2>访问令牌</h2>' +
      '<button class="btn btn--sm" id="btn-new-token">新建令牌</button></div>' +
      '<div class="card__body" id="token-body"><div class="empty"><strong>加载中</strong><span>读取令牌列表</span></div></div></section>' +
      '<section class="card" style="margin-top:16px"><div class="card__head"><h2>运行日志</h2>' +
      '<div class="row"><select id="log-lines" class="btn btn--ghost btn--sm">' +
      '<option value="200">200 行</option><option value="500">500 行</option><option value="1000">1000 行</option>' +
      '</select><button class="btn btn--sm btn--ghost" id="btn-reload-logs">刷新</button></div></div>' +
      '<div class="card__body"><div class="logbox mono" id="log-body">加载中…</div></div></section>' +
      '<section class="card" style="margin-top:16px"><div class="card__head"><h2>客户端公钥</h2>' +
      '<button class="btn btn--sm btn--ghost" id="copy-pub">复制</button></div><div class="card__body">' +
      '<p class="hint">打包客户端时必须内置同一把公钥（Resources/public.key）；换了密钥而客户端没换，所有授权码都会失效。</p>' +
      '<div class="code" id="pub-key">' + esc((state.meta && state.meta.public_key) || "—") + "</div>" +
      "</div></section>";

    $("#btn-refresh-system").addEventListener("click", function () {
      loadSystemFiles();
      loadTokens();
      loadLogs();
    });
    $("#btn-backup").addEventListener("click", downloadBackup);
    $("#btn-restore").addEventListener("click", function () { $("#restore-file").click(); });
    $("#restore-file").addEventListener("change", uploadRestore);
    $("#btn-new-token").addEventListener("click", openTokenModal);
    $("#copy-pub").addEventListener("click", function () {
      copyText((state.meta && state.meta.public_key) || "", "公钥");
    });
    $("#btn-reload-logs").addEventListener("click", loadLogs);
    $("#log-lines").addEventListener("change", loadLogs);

    loadSystemFiles();
    loadTokens();
    loadLogs();
  }

  function loadSystemFiles() {
    var box = $("#system-files");
    if (!box) return;
    apiGet("/api/system/status").then(function (st) {
      var rows = "";
      (st.files || []).forEach(function (f) {
        rows += "<dt>" + esc(f.name) + "</dt><dd>" +
          (f.exists ? fmtBytes(f.size_bytes) + ' · <span class="muted">' + fmtTime(f.mod_time) + "</span>" : '<span class="muted">不存在</span>') +
          "</dd>";
      });
      rows += "<dt>技能包</dt><dd>" + (st.skills ? st.skills.files + " 个文件 · " + fmtBytes(st.skills.size_bytes) : "—") + "</dd>";
      rows += "<dt>发行包</dt><dd>" + (st.releases ? st.releases.files + " 个文件 · " + fmtBytes(st.releases.size_bytes) : "—") + "</dd>";
      rows += "<dt>数据目录</dt><dd class=\"mono\" style=\"word-break:break-all\">" + esc(st.data_dir) + "</dd>";
      box.innerHTML = '<dl class="kv">' + rows + "</dl>";

      var note = $("#restore-note");
      if (note) {
        note.innerHTML = st.pending_restore
          ? '<div class="kv" style="margin-top:12px"><p class="hint">⚠ 有一个恢复包等待生效（' +
            esc(fmtTime(st.staged_at)) + " 暂存，" + (st.staged_entries || []).length +
            " 个条目）。<strong>重启管理端后生效。</strong></p></div>"
          : "";
      }
    }).catch(function (err) {
      box.innerHTML = '<div class="empty"><strong>读取失败</strong><span>' + esc(err.message) + "</span></div>";
    });
  }

  function downloadBackup() {
    var include = $("#backup-releases").checked ? "?include=releases" : "";
    var btn = $("#btn-backup");
    btn.disabled = true;
    btn.textContent = "正在打包…";

    // 原生窗口（WKWebView）没有下载代理：<a download> 点了没反应也不报错，
    // 用户只会以为按钮坏了。所以窗口版改由 Go 直接把备份写到 ~/Downloads，
    // 再把路径回显出来。浏览器版仍走原来的 blob 下载。
    if (window.__licadminNative && typeof window.licadminSaveBackup === "function") {
      window.licadminSaveBackup(!!$("#backup-releases").checked)
        .then(function (path) {
          toast("备份已保存到 " + path);
        })
        .catch(function (err) {
          toast("备份失败：" + (err && err.message ? err.message : err));
        })
        .then(function () {
          btn.disabled = false;
          btn.textContent = "下载备份";
        });
      return;
    }

    authFetch("/api/system/backup" + include, null, { raw: true })
      .then(function (res) {
        if (!res.ok) throw new Error("备份失败（HTTP " + res.status + "）");
        var name = "licadmin-backup.zip";
        var cd = res.headers.get("Content-Disposition") || "";
        var m = /filename="([^"]+)"/.exec(cd);
        if (m) name = m[1];
        return res.blob().then(function (blob) {
          var url = URL.createObjectURL(blob);
          var a = document.createElement("a");
          a.href = url;
          a.download = name;
          document.body.appendChild(a);
          a.click();
          a.remove();
          setTimeout(function () { URL.revokeObjectURL(url); }, 2000);
          toast("备份已开始下载：" + name);
        });
      })
      .catch(function (err) { toast("备份失败：" + err.message); })
      .then(function () {
        btn.disabled = false;
        btn.textContent = "下载备份";
      });
  }

  function uploadRestore(ev) {
    var file = ev.target.files && ev.target.files[0];
    ev.target.value = "";
    if (!file) return;
    openModal({
      title: "确认恢复？",
      body: '<p class="hint">将上传 <span class="mono">' + esc(file.name) + "</span>（" + fmtBytes(file.size) + "）。</p>" +
        '<p class="hint">恢复包会先暂存，<strong>重启管理端后生效</strong>；启动时现有数据会被整体挪到 ' +
        '<span class="mono">backup-before-restore-&lt;时间&gt;/</span>，可以据此退回。</p>',
      actions: [
        { label: "取消" },
        {
          label: "上传并暂存",
          primary: true,
          onClick: function () {
            authFetch("/api/system/restore", null, { raw: true, method: "POST", body: file })
              .then(function (res) {
                return res.json().then(function (data) {
                  if (!res.ok) throw new Error(data.error || "上传失败");
                  toast(data.message || "已暂存，重启后生效");
                  loadSystemFiles();
                });
              })
              .catch(function (err) { toast("恢复失败：" + err.message); });
            return false;
          }
        }
      ]
    });
  }

  function loadTokens() {
    var box = $("#token-body");
    if (!box) return;
    apiGet("/api/tokens").then(function (data) {
      var list = data.tokens || [];
      var head = '<p class="hint">令牌供脚本/CI 调用：可发码、可只读技能与发行包，' +
        "<strong>不能改账号、不能备份恢复</strong>。明文只在创建时显示一次。" +
        (data.static_token_enabled ? '（当前还启用着 -token 静态令牌，它视为全用途。）' : "") + "</p>";
      if (!list.length) {
        box.innerHTML = head + '<div class="empty"><strong>还没有令牌</strong><span>点右上角「新建令牌」</span></div>';
        return;
      }
      var rows = "";
      list.forEach(function (tk) {
        rows += "<tr>" +
          '<td><div class="cell"' + '><span class="mono">' + esc(tk.label) + "</span>" +
          '<span class="muted mono">' + esc(tk.prefix) + "…</span></div></td>" +
          "<td>" + (tk.scopes || []).map(function (sc) { return '<span class="pill pill--ok"><i></i>' + esc(scopeLabel(sc)) + "</span>"; }).join(" ") + "</td>" +
          '<td class="mono">' + fmtTime(tk.created_at) + "</td>" +
          '<td class="mono">' + (tk.last_used_at ? fmtTime(tk.last_used_at) : '<span class="muted">未使用</span>') + "</td>" +
          '<td class="cell-actions"><button class="btn btn--sm btn--ghost" data-revoke="' + tk.id + '" data-label="' + esc(tk.label) + '">撤销</button></td>' +
          "</tr>";
      });
      box.innerHTML = head +
        '<div class="tablewrap"><table class="table"><thead><tr>' +
        "<th>名称</th><th>用途</th><th>创建时间</th><th>最近使用</th><th></th>" +
        "</tr></thead><tbody>" + rows + "</tbody></table></div>";
      $$("[data-revoke]").forEach(function (btn) {
        btn.addEventListener("click", function () {
          var id = btn.getAttribute("data-revoke");
          var label = btn.getAttribute("data-label");
          openModal({
            title: "撤销令牌",
            body: '<p class="hint">撤销后「' + esc(label) + "」立刻失效，正在跑的脚本会收到 401。</p>",
            actions: [
              { label: "取消" },
              {
                label: "撤销", danger: true, primary: true, onClick: function () {
                  api("/api/tokens/" + id, { method: "DELETE" })
                    .then(function () { toast("已撤销"); loadTokens(); })
                    .catch(function (err) { toast("撤销失败：" + err.message); });
                  return false;
                }
              }
            ]
          });
        });
      });
    }).catch(function (err) {
      box.innerHTML = '<div class="empty"><strong>读取失败</strong><span>' + esc(err.message) + "</span></div>";
    });
  }

  function openTokenModal() {
    var scopes = (state.meta && state.meta.scopes) || ["licenses", "skills", "releases"];
    var opts = scopes.map(function (sc) {
      return '<label class="check"><input type="checkbox" name="token-scope" value="' + esc(sc) + '"' +
        (sc === "licenses" ? " checked" : "") + "> <span>" + esc(scopeLabel(sc)) + "</span></label>";
    }).join("");
    openModal({
      title: "新建访问令牌",
      body: '<div class="field"><label for="token-label">名称（给谁用）</label>' +
        '<input id="token-label" placeholder="例如：交付脚本 / Jenkins"></div>' +
        '<div class="field"><label>用途</label>' + opts + "</div>" +
        '<p class="hint">至少选一项。授权类用途能发码（含永久码），只读类用途仅能查列表。</p>',
      actions: [
        { label: "取消" },
        {
          label: "创建", primary: true, onClick: function () {
            var label = ($("#token-label").value || "").trim();
            var picked = [];
            $$('input[name="token-scope"]').forEach(function (el) { if (el.checked) picked.push(el.value); });
            if (!label) { toast("请填写名称"); return true; }
            if (!picked.length) { toast("至少选一个用途"); return true; }
            apiPost("/api/tokens", { label: label, scopes: picked })
              .then(function (data) {
                loadTokens();
                openModal({
                  title: "令牌已创建",
                  body: '<p class="hint">明文只显示这一次，关掉就没法再看到：</p>' +
                    '<div class="code" id="new-token">' + esc(data.plain_token) + "</div>" +
                    '<div class="row" style="margin-top:10px"><button class="btn btn--sm" id="btn-copy-new">复制令牌</button></div>',
                  actions: [{ label: "关闭", primary: true }],
                  onOpen: function () {
                    $("#btn-copy-new").addEventListener("click", function () {
                      copyText(data.plain_token, "令牌");
                    });
                  }
                });
              })
              .catch(function (err) { toast("创建失败：" + err.message); });
            return false;
          }
        }
      ]
    });
  }

  function loadLogs() {
    var box = $("#log-body");
    if (!box) return;
    var n = ($("#log-lines") && $("#log-lines").value) || "200";
    apiGet("/api/system/logs?lines=" + encodeURIComponent(n)).then(function (data) {
      if (!data.exists) {
        box.innerHTML = '<span class="muted">当前没有日志文件（命令行模式只输出到终端）。</span>';
        return;
      }
      var lines = data.lines || [];
      box.innerHTML = '<div class="muted" style="margin-bottom:6px">' + esc(data.path) + " · " + fmtBytes(data.size_bytes) + "</div>" +
        esc(lines.join("\n"));
      box.scrollTop = box.scrollHeight;
    }).catch(function (err) {
      box.innerHTML = '<span class="muted">读取失败：' + esc(err.message) + "</span>";
    });
  }

  function scopeLabel(sc) {
    return ({ licenses: "授权码", skills: "技能", releases: "发行包" })[sc] || sc;
  }

  // ---------- 视图：账号与安全 ----------

  // 「包格式与校验」是从技能页原来的说明卡拆出来的独立页：
  // 说明属于规范，不该和操作（上传/发布/导出）挤在同一屏。
  function viewSkillSpec() {
    var main = $("#view");
    main.innerHTML =
      pageHead("包格式与校验",
        "技能包就是一个 zip：一个目录一个技能，目录里必须有 <code>SKILL.md</code>；上传时会先校验再入库",
        '<button class="btn" id="btn-spec-upload">上传技能包</button>' +
        '<button class="btn btn--ghost" id="btn-spec-skills">返回技能库</button>') +
      '<div class="grid-2">' +
      '<section class="card"><div class="card__head"><h2>目录结构</h2></div><div class="card__body">' +
      '<pre class="code">office-docs/\n' +
      "  SKILL.md            # 必需：frontmatter 写 name / description\n" +
      "  manifest.json       # 可选：{\"name\":...,\"version\":\"1.2.0\"}\n" +
      "  scripts/build.sh    # 可选：随技能下发的脚本\n" +
      "  references/style.md # 可选：参考资料，客户端会原样装入\n" +
      "another-skill/\n  SKILL.md</pre>" +
      '<p class="hint">一个 zip 里可以放多个技能目录；没有 <code>manifest.json</code> 时版本记为 0.0.0。</p>' +
      "</div></section>" +
      '<section class="card"><div class="card__head"><h2>校验规则</h2></div>' +
      '<div class="table-wrap"><table class="table"><thead><tr><th>级别</th><th>规则</th></tr></thead><tbody>' +
      '<tr><td><span class="pill pill--danger"><i></i>error</span></td><td>路径穿越（<code>../</code>）、绝对路径、符号链接、缺少 <code>SKILL.md</code>、frontmatter 没有 <code>name</code></td></tr>' +
      '<tr><td><span class="pill pill--danger"><i></i>error</span></td><td>脚本里出现 <code>sudo</code>、<code>rm -rf /</code> 这类破坏性命令</td></tr>' +
      '<tr><td><span class="pill pill--warn"><i></i>warn</span></td><td>出现 <code>curl</code>、<code>chmod 777</code>、指向系统目录的绝对路径（可导入，但会记录告警）</td></tr>' +
      '<tr><td><span class="pill pill--off"><i></i>info</span></td><td>Python 脚本用 <code>python3 -m py_compile</code> 做语法检查；机器上没有 python3 时降级为跳过</td></tr>' +
      "</tbody></table></div></section></div>" +
      '<div class="grid-2" style="margin-top:14px">' +
      '<section class="card"><div class="card__head"><h2>包体上限</h2></div><div class="card__body">' +
      '<dl class="kv">' +
      "<dt>压缩包</dt><dd>32 MB</dd>" +
      "<dt>解包后</dt><dd>64 MB</dd>" +
      "<dt>文件数</dt><dd>400 个</dd>" +
      "<dt>单文件</dt><dd>8 MB</dd>" +
      "</dl></div></section>" +
      '<section class="card"><div class="card__head"><h2>发布语义</h2></div><div class="card__body">' +
      '<dl class="kv">' +
      "<dt>草稿</dt><dd>可以反复上传、校验、删除，客户端看不到。</dd>" +
      "<dt>发布</dt><dd>同名技能只保留一个已发布版本，发布新版本会自动把旧版本退回草稿。</dd>" +
      "<dt>打包</dt><dd>导出发布包后，客户端打包时把「已发布」的技能塞进 <code>Resources/skills</code>，首启装入 <code>~/.weknora/skills</code>。</dd>" +
      "</dl></div></section></div>";

    $("#btn-spec-upload").addEventListener("click", pickSkillZip);
    $("#btn-spec-skills").addEventListener("click", function () { location.hash = "#/skills"; });
  }

  // 「更新通道」页把原来塞在发布包页底部的接入说明拆出来：
  // 它是配置事实（地址、环境变量），跟上传/发布动作不是一回事。
  function viewChannel() {
    var main = $("#view");
    main.innerHTML =
      pageHead("更新通道",
        "客户端如何发现新版本、从哪个地址下载；这些参数在打包时注入，运行期可临时覆盖",
        '<button class="btn btn--ghost" id="btn-channel-releases">去发布包</button>') +
      '<section class="card"><div class="card__head"><h2>接入参数</h2></div><div class="card__body">' +
      '<dl class="kv">' +
      "<dt>管理端地址</dt><dd><code>" + esc(location.origin) + "</code>（客户端按同源拼 <code>/api/updates/...</code>）</dd>" +
      "<dt>打包注入</dt><dd><code>UPDATE_BASE_URL=" + esc(location.origin) + " bash scripts/oem-build-lite.sh</code><br>" +
      '<span class="hint">等价于 ldflags 的 <code>-X main.updateBaseURL=…</code>；不注入则回落到 GitHub 仓库（开发自测用）</span></dd>' +
      "<dt>运行期覆盖</dt><dd><code>WK_UPDATE_BASE_URL</code>、<code>WK_UPDATE_CHANNEL</code> 两个环境变量优先于编译期注入</dd>" +
      "<dt>通道取值</dt><dd><code>stable</code>、<code>beta</code>；非法值回落到 stable</dd>" +
      "</dl></div></section>" +
      '<section class="card"><div class="card__head"><h2>检查与下载</h2></div>' +
      '<div class="table-wrap"><table class="table"><thead><tr><th>接口</th><th>说明</th></tr></thead><tbody>' +
      "<tr><td class=\"mono\">GET /api/updates/latest</td><td>参数 <code>platform</code>、<code>arch</code>、<code>channel</code>、<code>current</code>；只返回该平台已发布的最高版本，" +
      "没有发布版本时 200 + <code>reason</code>，客户端据此区分「未配置」与「已最新」</td></tr>" +
      "<tr><td class=\"mono\">GET /api/updates/download/&lt;id&gt;</td><td>只下发已发布的包；响应头带 <code>X-Content-SHA256</code>、<code>X-Release-Version</code></td></tr>" +
      "</tbody></table></div>" +
      '<div class="card__body"><p class="hint">客户端拿到版本号后会再比一次语义化版本（防服务端脏数据），确认更高才提示下载；' +
      "下载完成后复用既有安装逻辑（.dmg / .zip / .exe）。</p></div></section>" +
      '<section class="card"><div class="card__head"><h2>边界</h2></div><div class="card__body">' +
      '<dl class="kv">' +
      "<dt>不鉴权</dt><dd>安装包里没有可信账号，要求令牌就只能硬编码进包，泄漏等于对全客户公开；因此这两个接口刻意不鉴权</dd>" +
      "<dt>暴露面</dt><dd>只暴露版本号、更新说明、sha256 与包体，不含台账、授权码或密钥；但仍应仅在回环或内网可达</dd>" +
      "</dl></div></section>";

    $("#btn-channel-releases").addEventListener("click", function () { location.hash = "#/releases"; });
  }

  function viewAccount() {
    var main = $("#view");
    var loggedIn = state.auth.authenticated && state.auth.method === "session";
    var rows =
      "<dt>账号</dt><dd class=\"mono\">" + esc(state.auth.username || "—") + "</dd>" +
      "<dt>登录方式</dt><dd>" + (state.auth.method === "token" ? "自动化令牌（脚本）" : "账号密码（会话 Cookie）") + "</dd>" +
      "<dt>会话有效期</dt><dd>12 小时，剩余不足 6 小时时自动续期</dd>" +
      "<dt>数据文件</dt><dd class=\"mono\">licadmin-data/licadmin.db（账号与审计）</dd>";

    main.innerHTML =
      '<div class="view-head"><div><h1>账号与安全</h1><p>登录账号、密码与操作审计</p></div>' +
      '<button class="btn btn--ghost" id="btn-logout"' + (state.auth.authenticated ? "" : " hidden") + ">退出登录</button></div>" +
      '<div class="grid-2">' +
      '<section class="card"><div class="card__head"><h2>当前账号</h2></div><div class="card__body">' +
      '<dl class="kv">' + rows + "</dl>" +
      (loggedIn ? "" : '<p class="hint">当前不是账号登录，无法修改密码。请退出后用管理员账号登录。</p>') +
      "</div></section>" +
      '<section class="card"><div class="card__head"><h2>修改密码</h2></div><div class="card__body">' +
      (loggedIn
        ? '<div class="field"><label for="pw-current">当前密码</label>' +
          '<input type="password" id="pw-current" autocomplete="current-password"></div>' +
          '<div class="field"><label for="pw-next">新密码</label>' +
          '<input type="password" id="pw-next" autocomplete="new-password" placeholder="至少 10 位，含字母与数字"></div>' +
          '<div class="field"><label for="pw-next2">确认新密码</label>' +
          '<input type="password" id="pw-next2" autocomplete="new-password"></div>' +
          '<p class="hint" id="pw-hint">改密后其他浏览器的登录会立刻失效。</p>' +
          '<div class="row"><button class="btn" id="btn-save-password">保存新密码</button></div>'
        : '<div class="empty"><strong>需要账号登录</strong><span>自动化令牌只能管理授权码</span></div>') +
      "</div></section></div>" +
      '<section class="card" style="margin-top:16px"><div class="card__head"><h2>操作审计</h2>' +
      '<button class="btn btn--sm btn--ghost" id="btn-reload-audit">刷新</button></div>' +
      '<div class="card__body" id="audit-body"><div class="empty"><strong>加载中</strong><span>读取最近操作记录</span></div></div></section>';

    $("#btn-logout").addEventListener("click", function () {
      authFetch("/api/auth/logout", {}).then(function () {
        state.auth.authenticated = false;
        state.auth.method = "";
        $("#gate").hidden = true;
        showGate("login");
        toast("已退出登录");
      });
    });

    if (loggedIn) {
      $("#btn-save-password").addEventListener("click", function () {
        var cur = $("#pw-current").value || "";
        var next = $("#pw-next").value || "";
        var next2 = $("#pw-next2").value || "";
        var hint = $("#pw-hint");
        if (!cur) { hint.textContent = "请填写当前密码"; return; }
        if (next.length < 10) { hint.textContent = "新密码至少 10 位"; return; }
        if (next !== next2) { hint.textContent = "两次输入的新密码不一致"; return; }
        apiPost("/api/auth/password", { current: cur, next: next }).then(function () {
          $("#pw-current").value = ""; $("#pw-next").value = ""; $("#pw-next2").value = "";
          hint.textContent = "已更新。其他浏览器的会话已失效。";
          toast("密码已更新");
          loadAudit();
        }).catch(function (err) { if (err.message !== "unauthorized") hint.textContent = err.message; });
      });
    }

    $("#btn-reload-audit").addEventListener("click", loadAudit);
    loadAudit();
  }

  function loadAudit() {
    var box = $("#audit-body");
    if (!box) return;
    if (state.auth.method !== "session") {
      box.innerHTML = '<div class="empty"><strong>需要账号登录</strong><span>审计日志只对登录会话开放</span></div>';
      return;
    }
    apiGet("/api/audit?limit=100").then(function (res) {
      var rows = (res.entries || []);
      if (!rows.length) {
        box.innerHTML = '<div class="empty"><strong>还没有记录</strong><span>登录、发码、续期、作废都会留痕</span></div>';
        return;
      }
      box.innerHTML =
        '<div class="table-wrap"><table class="table"><thead><tr>' +
        "<th>时间</th><th>操作者</th><th>动作</th><th>对象</th><th>说明</th><th>来源</th>" +
        "</tr></thead><tbody>" +
        rows.map(function (e) {
          return "<tr><td class=\"mono\">" + esc(fmtTime(e.ts)) + "</td>" +
            "<td>" + esc(e.actor) + "</td>" +
            "<td>" + esc(e.action) + "</td>" +
            "<td class=\"mono\">" + esc(e.target || "—") + "</td>" +
            "<td>" + esc(e.detail || "—") + "</td>" +
            "<td class=\"mono\">" + esc(e.ip || "—") + "</td></tr>";
        }).join("") +
        "</tbody></table></div>";
    }).catch(function (err) {
      if (err.message === "unauthorized") return;
      box.innerHTML = errorBlock(err.message);
    });
  }

  // ---------- 视图：技能管理（阶段 3） ----------

  var SKILL_STATUS = {
    draft: { label: "草稿", cls: "pill--off" },
    published: { label: "已发布", cls: "pill--ok" }
  };

  function fmtBytes(n) {
    if (!n) return "0 B";
    if (n < 1024) return n + " B";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
    return (n / 1024 / 1024).toFixed(1) + " MB";
  }

  function viewSkills() {
    var main = $("#view");
    main.innerHTML =
      '<div class="view-head"><div><h1>技能管理</h1>' +
      "<p>上传技能包、校验、发布；客户端打包时会把「已发布」的技能塞进 Resources/skills</p></div>" +
      '<div class="row"><button class="btn btn--ghost" id="btn-skill-bundle">导出发布包</button>' +
      '<button class="btn" id="btn-skill-upload">上传技能包</button></div></div>' +
      '<section class="card"><div class="card__head"><h2>技能库</h2>' +
      '<span class="muted">同名技能只保留一个已发布版本，发布新版本会自动把旧的退回草稿</span></div>' +
      '<div id="skill-table"><div class="empty">正在加载…</div></div>' +
      '<div class="pager"><span>包格式、校验规则与包体上限见 <a href="#/skill-spec" class="muted">包格式与校验</a></span></div></section>';

    $("#btn-skill-upload").addEventListener("click", pickSkillZip);
    $("#btn-skill-bundle").addEventListener("click", downloadSkillBundle);
    loadSkills();
  }

  function loadSkills() {
    var box = $("#skill-table");
    if (!box) return;
    apiGet("/api/skills").then(function (data) {
      renderSkillTable(data.skills || []);
    }).catch(function (err) {
      if (err.message !== "unauthorized") box.innerHTML = errorBlock(err.message);
    });
  }

  function skillStatusPill(p) {
    var meta = SKILL_STATUS[p.status] || { label: p.status, cls: "pill--off" };
    var extra = p.checked_ok ? "" : " · 校验未通过";
    return '<span class="pill ' + meta.cls + '"><i></i>' + esc(meta.label + extra) + "</span>";
  }

  function renderSkillTable(rows) {
    var box = $("#skill-table");
    if (!rows.length) {
      box.innerHTML = '<div class="empty"><strong>还没有技能包</strong>' +
        "<span>点右上角「上传技能包」，把一个技能目录打成 zip 传上来。</span></div>";
      return;
    }
    box.innerHTML = '<div class="table-wrap"><table class="table"><thead><tr>' +
      "<th>技能</th><th>版本</th><th>状态</th><th>文件</th><th>大小</th><th>上传时间</th><th></th>" +
      "</tr></thead><tbody>" +
      rows.map(function (p) {
        return "<tr>" +
          "<td>" + esc(p.name) +
          '<div class="muted mono" style="font-size:11px">' + esc(p.description || p.title || "—") + "</div></td>" +
          '<td class="mono">' + esc(p.version) + "</td>" +
          "<td>" + skillStatusPill(p) + "</td>" +
          '<td class="mono">' + p.file_count + "</td>" +
          '<td class="mono">' + fmtBytes(p.size_bytes) + "</td>" +
          '<td class="mono">' + esc(fmtTime(p.uploaded_at)) + "</td>" +
          '<td class="nowrap"><button class="btn btn--sm btn--ghost" data-skill="' + p.id + '">详情</button></td>' +
          "</tr>";
      }).join("") + "</tbody></table></div>";

    $$("[data-skill]", box).forEach(function (btn) {
      btn.addEventListener("click", function () {
        openSkillDetail(parseInt(btn.getAttribute("data-skill"), 10));
      });
    });
  }

  // pickSkillZip 每次新建 input：复用同一个 input 时，选同一个文件不会触发 change。
  function pickSkillZip() {
    var input = document.createElement("input");
    input.type = "file";
    input.accept = ".zip,application/zip";
    input.addEventListener("change", function () {
      var file = input.files && input.files[0];
      if (file) validateSkillZip(file);
    });
    input.click();
  }

  function validateSkillZip(file) {
    uploadSkillZip("/api/skills/validate", file).then(function (res) {
      var findings = res.findings || [];
      var rows = findings.length
        ? findings.map(function (f) {
            var cls = f.level === "error" ? "pill--danger" : (f.level === "warn" ? "pill--warn" : "pill--off");
            return "<tr><td>" + '<span class="pill ' + cls + '"><i></i>' + esc(f.level) + "</span></td>" +
              '<td class="mono">' + esc(f.code) + "</td>" +
              "<td>" + esc(f.message) + (f.path ? '<div class="muted mono" style="font-size:11px">' + esc(f.path) + "</div>" : "") + "</td></tr>";
          }).join("")
        : '<tr><td colspan="3" class="muted">没有发现问题</td></tr>';

      openModal({
        title: "校验结果 · " + (res.name || file.name),
        body:
          '<dl class="kv">' +
          "<dt>技能</dt><dd>" + esc(res.name) + "</dd>" +
          "<dt>版本</dt><dd class=\"mono\">" + esc(res.version || "0.0.0") + "</dd>" +
          "<dt>文件</dt><dd class=\"mono\">" + res.file_count + " 个 · " + fmtBytes(res.size_bytes) + "</dd>" +
          "<dt>结论</dt><dd>" + (res.ok
            ? '<span class="pill pill--ok"><i></i>可以导入</span>'
            : '<span class="pill pill--danger"><i></i>有 ' + res.error_count + " 个错误</span>") + "</dd>" +
          "</dl><div class=\"divider\"></div>" +
          '<div class="table-wrap"><table class="table"><thead><tr><th>级别</th><th>代码</th><th>说明</th></tr></thead><tbody>' +
          rows + "</tbody></table></div>",
        actions: [
          {
            label: res.ok ? "导入技能库" : "仍然导入（留档）", primary: res.ok, onClick: function () {
              uploadSkillZip("/api/skills/upload", file).then(function (out) {
                toast("已导入 " + out.skill.name + " " + out.skill.version);
                loadSkills();
              }).catch(function (err) { toast(err.message); });
              return false;
            }
          },
          { label: "关闭", primary: !res.ok }
        ]
      });
    }).catch(function (err) {
      if (err.message !== "unauthorized") toast(err.message);
    });
  }

  // uploadSkillZip 用 multipart 上传，不能走 api()（那里固定发 JSON）。
  function uploadSkillZip(path, file) {
    var fd = new FormData();
    fd.append("file", file);
    var headers = {};
    if (state.token) headers["X-Licadmin-Token"] = state.token;
    return fetch(path, { method: "POST", headers: headers, body: fd }).then(function (res) {
      if (res.status === 401) {
        if (state.auth.enabled) showGate("login"); else askToken("口令无效或已更换，请重新输入。");
        throw new Error("unauthorized");
      }
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok) throw new Error((data && data.error) || ("上传失败（HTTP " + res.status + "）"));
        return data;
      });
    });
  }

  function openSkillDetail(id) {
    apiGet("/api/skills/" + id).then(function (out) {
      var p = out.skill;
      var findings = p.findings || [];
      var rows = findings.length
        ? findings.map(function (f) {
            var cls = f.level === "error" ? "pill--danger" : (f.level === "warn" ? "pill--warn" : "pill--off");
            return "<tr><td>" + '<span class="pill ' + cls + '"><i></i>' + esc(f.level) + "</span></td>" +
              '<td class="mono">' + esc(f.code) + "</td><td>" + esc(f.message) +
              (f.path ? '<div class="muted mono" style="font-size:11px">' + esc(f.path) + "</div>" : "") + "</td></tr>";
          }).join("")
        : '<tr><td colspan="3" class="muted">校验时没有发现问题</td></tr>';

      openModal({
        title: "技能详情 · " + p.name,
        body:
          '<dl class="kv">' +
          "<dt>状态</dt><dd>" + skillStatusPill(p) + "</dd>" +
          "<dt>名称</dt><dd class=\"mono\">" + esc(p.name) + "</dd>" +
          "<dt>版本</dt><dd class=\"mono\">" + esc(p.version) + "</dd>" +
          "<dt>标题</dt><dd>" + esc(p.title || "—") + "</dd>" +
          "<dt>描述</dt><dd>" + esc(p.description || "—") + "</dd>" +
          "<dt>作者</dt><dd>" + esc(p.author || "—") + "</dd>" +
          "<dt>文件</dt><dd class=\"mono\">" + p.file_count + " 个 · " + fmtBytes(p.size_bytes) + "</dd>" +
          "<dt>SHA256</dt><dd class=\"mono\">" + esc(p.sha256) + "</dd>" +
          "<dt>上传</dt><dd>" + esc(fmtTime(p.uploaded_at)) + "</dd>" +
          (p.published_at ? "<dt>发布</dt><dd>" + esc(fmtTime(p.published_at)) + "</dd>" : "") +
          (p.dir ? "<dt>目录</dt><dd class=\"mono\">" + esc(p.dir) + "</dd>" : "") +
          "</dl><div class=\"divider\"></div>" +
          '<div class="table-wrap"><table class="table"><thead><tr><th>级别</th><th>代码</th><th>说明</th></tr></thead><tbody>' +
          rows + "</tbody></table></div>",
        actions: [
          p.status === "published"
            ? { label: "撤回发布", onClick: function () { skillAction(id, "unpublish"); return false; } }
            : { label: "发布", primary: true, onClick: function () { skillAction(id, "publish"); return false; } },
          { label: "删除", danger: true, onClick: function () { skillAction(id, "delete"); return false; } },
          { label: "关闭", primary: p.status === "published" }
        ]
      });
    }).catch(function (err) {
      if (err.message !== "unauthorized") toast(err.message);
    });
  }

  function skillAction(id, action) {
    apiPost("/api/skills/" + id + "/" + action, {}).then(function () {
      toast(action === "publish" ? "已发布" : (action === "unpublish" ? "已撤回" : "已删除"));
      closeModal();
      loadSkills();
    }).catch(function (err) {
      if (err.message !== "unauthorized") toast(err.message);
    });
  }

  // downloadSkillBundle 与导出台账同理：带凭据请求，再用 blob 触发下载。
  function downloadSkillBundle() {
    var headers = {};
    if (state.token) headers["X-Licadmin-Token"] = state.token;
    fetch("/api/skills/bundle", { headers: headers }).then(function (res) {
      if (res.status === 401) {
        if (state.auth.enabled) showGate("login"); else askToken("口令无效或已更换，请重新输入。");
        return null;
      }
      if (!res.ok) {
        return res.json().catch(function () { return {}; }).then(function (data) {
          throw new Error((data && data.error) || ("导出失败（HTTP " + res.status + "）"));
        });
      }
      return res.blob();
    }).then(function (blob) {
      if (!blob) return;
      var url = URL.createObjectURL(blob);
      var a = document.createElement("a");
      a.href = url;
      a.download = "skills-bundle-" + new Date().toISOString().slice(0, 10) + ".zip";
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
      toast("发布包已导出");
    }).catch(function (err) {
      if (err.message !== "unauthorized") toast(err.message);
    });
  }


  // ---------- 视图：版本发布（阶段 4） ----------

  var RELEASE_STATUS = {
    draft: { label: "草稿", cls: "pill--off" },
    published: { label: "已发布", cls: "pill--ok" }
  };

  function releaseStatusPill(r) {
    var meta = RELEASE_STATUS[r.status] || { label: r.status, cls: "pill--off" };
    return '<span class="pill ' + meta.cls + '"><i></i>' + esc(meta.label) + "</span>";
  }

  function viewReleases() {
    var main = $("#view");
    main.innerHTML =
      '<div class="view-head"><div><h1>版本发布</h1>' +
      "<p>上传安装包并发布；客户端通过 /api/updates/latest 检查更新，落地后即可对全量客户生效</p></div>" +
      '<div class="row"><button class="btn btn--ghost" id="btn-release-refresh">刷新</button>' +
      '<button class="btn" id="btn-release-upload">上传发布包</button></div></div>' +
      '<section class="card"><div class="card__head"><h2>发布包</h2>' +
      '<span class="muted">同一平台 / 架构 / 通道只保留一个已发布版本，发布新版本会自动把旧的退回草稿</span></div>' +
      '<div id="release-table"><div class="empty">正在加载…</div></div>' +
      '<div class="pager"><span>客户端如何接入这个通道，见 <a href="#/channel" class="muted">更新通道</a></span></div></section>';

    $("#btn-release-upload").addEventListener("click", openReleaseUpload);
    $("#btn-release-refresh").addEventListener("click", loadReleases);
    loadReleases();
  }

  function loadReleases() {
    var box = $("#release-table");
    if (!box) return;
    apiGet("/api/releases").then(function (data) {
      renderReleaseTable(data.releases || []);
    }).catch(function (err) {
      if (err.message !== "unauthorized") box.innerHTML = errorBlock(err.message);
    });
  }

  function renderReleaseTable(rows) {
    var box = $("#release-table");
    if (!rows.length) {
      box.innerHTML = '<div class="empty"><strong>还没有发布包</strong>' +
        "<span>点右上角「上传发布包」，传一个安装包（zip / dmg / exe / AppImage）。</span></div>";
      return;
    }
    box.innerHTML = '<div class="table-wrap"><table class="table"><thead><tr>' +
      "<th>平台 / 架构</th><th>通道</th><th>版本</th><th>大小</th><th>状态</th><th>上传时间</th><th></th>" +
      "</tr></thead><tbody>" +
      rows.map(function (r) {
        var actions = '<button class="btn btn--sm btn--ghost" data-release-detail="' + r.id + '">详情</button>';
        if (r.status === "published") {
          actions += ' <button class="btn btn--sm btn--ghost" data-release-unpublish="' + r.id + '">撤回</button>';
        } else {
          actions += ' <button class="btn btn--sm" data-release-publish="' + r.id + '">发布</button>';
          actions += ' <button class="btn btn--sm btn--ghost" data-release-delete="' + r.id + '">删除</button>';
        }
        return "<tr>" +
          "<td><b>" + esc(r.platform) + "</b>" +
          '<div class="muted mono" style="font-size:11px">' + esc(r.arch) + "</div></td>" +
          '<td class="mono">' + esc(r.channel) + "</td>" +
          '<td class="mono">v' + esc(r.version) + "</td>" +
          '<td class="mono">' + fmtBytes(r.size_bytes) + "</td>" +
          "<td>" + releaseStatusPill(r) + "</td>" +
          '<td class="mono">' + esc(fmtTime(r.uploaded_at)) + "</td>" +
          '<td class="nowrap">' + actions + "</td>" +
          "</tr>";
      }).join("") + "</tbody></table></div>";

    $$("[data-release-detail]", box).forEach(function (btn) {
      btn.addEventListener("click", function () {
        openReleaseDetail(rows.filter(function (r) {
          return String(r.id) === btn.getAttribute("data-release-detail");
        })[0]);
      });
    });
    $$("[data-release-publish]", box).forEach(function (btn) {
      btn.addEventListener("click", function () {
        releaseAction(btn.getAttribute("data-release-publish"), "publish", "发布上线");
      });
    });
    $$("[data-release-unpublish]", box).forEach(function (btn) {
      btn.addEventListener("click", function () {
        releaseAction(btn.getAttribute("data-release-unpublish"), "unpublish", "撤回");
      });
    });
    $$("[data-release-delete]", box).forEach(function (btn) {
      btn.addEventListener("click", function () {
        releaseAction(btn.getAttribute("data-release-delete"), "delete", "删除");
      });
    });
  }

  function releaseAction(id, action, label) {
    apiPost("/api/releases/" + id + "/" + action).then(function () {
      toast(label + "成功");
      loadReleases();
    }).catch(function (err) {
      if (err.message !== "unauthorized") toast(err.message);
    });
  }

  function openReleaseDetail(r) {
    if (!r) return;
    openModal({
      title: "发布包 v" + r.version,
      body:
        '<dl class="kv">' +
        "<dt>平台</dt><dd><code>" + esc(r.platform + " / " + r.arch + " · " + r.channel) + "</code></dd>" +
        "<dt>文件</dt><dd><code>" + esc(r.filename) + "</code></dd>" +
        "<dt>大小</dt><dd>" + fmtBytes(r.size_bytes) + "</dd>" +
        "<dt>sha256</dt><dd><code style=\"word-break:break-all\">" + esc(r.sha256) + "</code></dd>" +
        "<dt>状态</dt><dd>" + releaseStatusPill(r) + "</dd>" +
        "<dt>上传 / 发布</dt><dd>" + esc(fmtTime(r.uploaded_at)) +
        (r.published_at ? " / " + esc(fmtTime(r.published_at)) : "") + "</dd>" +
        "<dt>更新说明</dt><dd>" + (r.notes ? esc(r.notes) : "—") + "</dd>" +
        "</dl>",
      actions: [{ label: "关闭" }]
    });
  }

  function openReleaseUpload() {
    openModal({
      title: "上传发布包",
      body:
        '<div class="form">' +
        '<label class="field"><span class="field__label">安装包</span>' +
        '<input class="input" type="file" id="release-file" accept=".zip,.dmg,.exe,.AppImage,.tar.gz"></label>' +
        '<div class="grid-2">' +
        '<label class="field"><span class="field__label">平台</span><select class="input" id="release-platform">' +
        '<option value="darwin">macOS</option><option value="windows">Windows</option><option value="linux">Linux</option>' +
        "</select></label>" +
        '<label class="field"><span class="field__label">架构</span><select class="input" id="release-arch">' +
        '<option value="arm64">arm64</option><option value="amd64">amd64</option><option value="universal">universal</option>' +
        "</select></label></div>" +
        '<div class="grid-2">' +
        '<label class="field"><span class="field__label">通道</span><select class="input" id="release-channel">' +
        '<option value="stable">stable</option><option value="beta">beta</option></select></label>' +
        '<label class="field"><span class="field__label">版本号</span>' +
        '<input class="input" id="release-version" placeholder="0.9.0（会自动从文件名里猜）"></label></div>' +
        '<label class="field"><span class="field__label">更新说明</span>' +
        '<textarea class="input" id="release-notes" rows="3" placeholder="这一版改了什么，客户端弹窗里会显示"></textarea></label>' +
        "</div>",
      actions: [
        { label: "上传", primary: true, onClick: function () { uploadRelease(); return true; } },
        { label: "取消" }
      ],
      onOpen: function () {
        // 从文件名猜版本号：国研智研体-0.9.1-macOS-arm64.zip → 0.9.1
        var file = $("#release-file");
        if (file) {
          file.addEventListener("change", function () {
            var name = (file.files && file.files[0] && file.files[0].name) || "";
            var m = /(\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?)/.exec(name);
            var v = $("#release-version");
            if (m && v && !v.value) v.value = m[1];
          });
        }
      }
    });
  }

  function uploadRelease() {
    var file = $("#release-file");
    if (!file || !file.files || !file.files[0]) {
      toast("请先选择安装包");
      return true;
    }
    var version = ($("#release-version").value || "").trim();
    if (!version) {
      toast("请填写版本号");
      return true;
    }
    var fd = new FormData();
    fd.append("file", file.files[0]);
    fd.append("platform", $("#release-platform").value);
    fd.append("arch", $("#release-arch").value);
    fd.append("channel", $("#release-channel").value);
    fd.append("version", version);
    fd.append("notes", $("#release-notes").value || "");

    fetch("/api/releases/upload", { method: "POST", body: fd, credentials: "same-origin" }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok) throw new Error((data && data.error) || ("上传失败（HTTP " + res.status + "）"));
        return data;
      });
    }).then(function () {
      toast("已上传，发布后客户端才能看到");
      closeModal();
      loadReleases();
    }).catch(function (err) {
      toast(err.message);
    });
    return true;
  }

  // ---------- 路由 ----------

  var routes = {
    overview: viewOverview,
    licenses: viewLicenses,
    issue: viewIssue,
    batch: viewIssue, // 合并前的入口，等价于 #/issue 的批量模式
    inspect: viewInspect,
    skills: viewSkills,
    "skill-spec": viewSkillSpec,
    releases: viewReleases,
    channel: viewChannel,
    account: viewAccount,
    system: viewSystem,
    settings: viewSystem // 老书签兼容
  };

  function currentRoute() {
    var m = /^#\/([a-z-]+)/.exec(location.hash || "");
    var name = m ? m[1] : "overview";
    return routes[name] ? name : "overview";
  }

  function route() {
    state.route = currentRoute();
    // 合并后 #/batch 没有独立导航项，高亮要落回「发码」。
    var activeRoute = state.route === "batch" ? "issue" : state.route;
    $$(".navlink[data-nav]").forEach(function (a) {
      a.classList.toggle("is-active", a.getAttribute("data-nav") === activeRoute);
    });
    // 当前页所属分组若被折叠就展开：否则用户看不到自己在哪里。
    var gid = groupOfRoute(state.route);
    var group = gid ? $('.nav-group[data-group="' + gid + '"]') : null;
    if (group && group.classList.contains("is-collapsed")) {
      group.classList.remove("is-collapsed");
      var head = $("[data-group-toggle]", group);
      if (head) head.setAttribute("aria-expanded", "true");
      var map = navCollapsed();
      map[gid] = false;
      saveNavCollapsed(map);
    }
    paintCrumb(state.route);
    var fn = routes[state.route];
    if (fn) fn();
    $("#view").focus();
  }

  function boot() {
    bootTheme();
    bootToken();
    renderNav();
    bindRail();
    bindTheme();
    $$("[data-modal-close]").forEach(function (el) {
      el.addEventListener("click", closeModal);
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && !$("#modal").hidden) closeModal();
    });
    $("#btn-token").addEventListener("click", function () {
      askToken("重新输入访问口令。");
    });
    window.addEventListener("hashchange", route);
    // 快捷键只注册一次：挂在视图挂载处会在每次进入发码页时叠加监听，
    // 结果是按一次 ⌘+Enter 发出多张码。
    document.addEventListener("keydown", issueHotkey);

    $("#btn-account").addEventListener("click", function () {
      location.hash = "#/account";
    });

    loadAuthStatus().then(function (auth) {
      if (!auth.enabled) {
        // 没配账号库（老用法）：退回口令模式，行为与阶段 1 一致。
        if (!state.token) { askToken("请输入访问口令。"); }
        return loadMeta().then(route);
      }
      if (auth.setupRequired) { showGate("setup"); return; }
      if (!auth.authenticated) { showGate("login"); return; }
      hideGate();
      paintAccountChip();
      return loadMeta().then(route);
    }).catch(function (err) {
      $("#view").innerHTML = errorBlock(err.message);
    });
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", boot);
  } else {
    boot();
  }
})();