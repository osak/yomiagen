/*!
 * AI Doc Template — doc.js
 * 依存なし。<script src="assets/doc.js" defer></script> でも、
 * <script>…中身をそのまま貼り付け…</script> でも動く。
 *
 * 提供する機能:
 *   - 見出しへの id 付与と目次の自動生成 + スクロール追従
 *   - 見出しアンカー（#リンク）
 *   - コードブロックのコピーボタン
 *   - 幅の広い表を横スクロール可能にラップ
 *   - Mermaid 図の描画（必要なときだけ CDN から遅延ロード）
 *   - TeX 数式の描画（KaTeX / MathJax、必要なときだけ遅延ロード）
 *   - highlight.js によるシンタックスハイライト（任意・既定は無効）
 *   - ライト/ダーク切替ボタン（任意・既定は無効）
 *
 * 設定は読み込み前に window.DOC_CONFIG を定義して上書きする。
 *   <script>window.DOC_CONFIG = { toc: { enable: false } };</script>
 *   <script src="assets/doc.js" defer></script>
 */
(function () {
  'use strict';

  /* ----------------------------------------------------------------------
     既定設定
     enable: true | false | 'auto'（'auto' = 対象要素があるときだけ有効）
     ---------------------------------------------------------------------- */
  var DEFAULTS = {
    content: '.doc-body',

    toc: {
      enable: true,
      target: '#doc-toc',
      headings: 'h2, h3',
      title: '目次',
      minHeadings: 2,
      scrollSpy: true
    },

    anchors: { enable: true, headings: 'h2, h3, h4', symbol: '#' },

    copyCode: {
      enable: true,
      label: 'コピー',
      copiedLabel: 'コピーしました',
      failedLabel: '失敗しました'
    },

    tableScroll: { enable: true },

    mermaid: {
      enable: 'auto',
      selector: '.mermaid',
      src: 'https://cdn.jsdelivr.net/npm/mermaid@11.17.2/dist/mermaid.min.js',
      config: {}
    },

    math: {
      enable: 'auto',
      engine: 'katex', // 'katex' | 'mathjax'
      katex: {
        js: 'https://cdnjs.cloudflare.com/ajax/libs/KaTeX/0.16.11/katex.min.js',
        autoRender: 'https://cdnjs.cloudflare.com/ajax/libs/KaTeX/0.16.11/contrib/auto-render.min.js',
        css: 'https://cdnjs.cloudflare.com/ajax/libs/KaTeX/0.16.11/katex.min.css',
        delimiters: [
          { left: '$$', right: '$$', display: true },
          { left: '\\[', right: '\\]', display: true },
          { left: '\\(', right: '\\)', display: false },
          { left: '$', right: '$', display: false }
        ],
        ignoredTags: ['script', 'noscript', 'style', 'textarea', 'pre', 'code', 'option']
      },
      mathjax: {
        js: 'https://cdnjs.cloudflare.com/ajax/libs/mathjax/3.2.2/es5/tex-mml-chtml.js'
      }
    },

    highlight: {
      enable: false,
      src: 'https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.10.0/highlight.min.js'
    },

    themeToggle: {
      enable: false,
      storageKey: 'doc-theme',
      lightLabel: 'ライトテーマに切り替え',
      darkLabel: 'ダークテーマに切り替え'
    }
  };

  /* ----------------------------------------------------------------------
     ユーティリティ
     ---------------------------------------------------------------------- */

  function isPlainObject(v) {
    return v !== null && typeof v === 'object' && !Array.isArray(v);
  }

  function merge(base, override) {
    var out = {};
    Object.keys(base).forEach(function (k) { out[k] = base[k]; });
    if (!isPlainObject(override)) return out;
    Object.keys(override).forEach(function (k) {
      out[k] = isPlainObject(base[k]) && isPlainObject(override[k])
        ? merge(base[k], override[k])
        : override[k];
    });
    return out;
  }

  var CFG = merge(DEFAULTS, window.DOC_CONFIG || {});

  function $(sel, root) { return (root || document).querySelector(sel); }
  function $$(sel, root) {
    return Array.prototype.slice.call((root || document).querySelectorAll(sel));
  }

  function enabled(flag, hasTargets) {
    if (flag === 'auto') return !!hasTargets;
    return !!flag;
  }

  // 同一オリジンや file:// のローカル配置では crossorigin を付けない
  // （付けると file:// で CORS エラーになり読めなくなる）
  function isCrossOrigin(url) {
    if (!/^https?:\/\//i.test(url)) return false;
    try { return new URL(url, location.href).origin !== location.origin; }
    catch (e) { return true; }
  }

  var scriptCache = {};
  function loadScript(src) {
    if (scriptCache[src]) return scriptCache[src];
    scriptCache[src] = new Promise(function (resolve, reject) {
      var s = document.createElement('script');
      s.src = src;
      s.async = true;
      if (isCrossOrigin(src)) s.crossOrigin = 'anonymous';
      s.onload = function () { resolve(); };
      s.onerror = function () { reject(new Error('failed to load ' + src)); };
      document.head.appendChild(s);
    });
    return scriptCache[src];
  }

  function loadStyle(href) {
    if (document.querySelector('link[href="' + href + '"]')) return;
    var l = document.createElement('link');
    l.rel = 'stylesheet';
    l.href = href;
    if (isCrossOrigin(href)) l.crossOrigin = 'anonymous';
    document.head.appendChild(l);
  }

  function warn(msg, err) {
    if (window.console && console.warn) console.warn('[doc.js] ' + msg, err || '');
  }

  function prefersDark() {
    var attr = document.documentElement.getAttribute('data-theme');
    if (attr === 'dark') return true;
    if (attr === 'light') return false;
    return !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches);
  }

  /* ----------------------------------------------------------------------
     見出し id（日本語をそのまま残す安全なスラッグ）
     ---------------------------------------------------------------------- */

  function slugify(text) {
    return String(text)
      .trim()
      .toLowerCase()
      .replace(/[\s　]+/g, '-')
      .replace(/[!"#$%&'()*+,./:;<=>?@[\]^`{|}~。、，．・「」『』（）〔〕【】〈〉《》！？：；]/g, '')
      .replace(/-+/g, '-')
      .replace(/^-|-$/g, '');
  }

  function ensureHeadingIds(headings) {
    var used = {};
    $$('[id]').forEach(function (el) { used[el.id] = true; });

    headings.forEach(function (h, i) {
      if (h.id) { used[h.id] = true; return; }
      var base = slugify(h.textContent) || ('section-' + (i + 1));
      var id = base;
      var n = 2;
      while (used[id]) { id = base + '-' + n; n += 1; }
      used[id] = true;
      h.id = id;
    });
  }

  /* ----------------------------------------------------------------------
     目次 + スクロール追従
     ---------------------------------------------------------------------- */

  function buildToc(content) {
    var host = $(CFG.toc.target);
    if (!host) return null;

    var headings = $$(CFG.toc.headings, content).filter(function (h) {
      return h.dataset.tocSkip === undefined;
    });

    if (headings.length < CFG.toc.minHeadings) {
      host.remove();
      return null;
    }

    ensureHeadingIds(headings);

    var scroller = document.createElement('div');
    scroller.className = 'doc-toc-scroller';

    var rootList = document.createElement('ul');
    var currentSub = null;

    headings.forEach(function (h) {
      var level = Number(h.tagName.charAt(1));
      var li = document.createElement('li');
      li.dataset.level = String(level);

      var a = document.createElement('a');
      a.href = '#' + h.id;
      a.textContent = h.textContent.replace(/^#\s*/, '').trim();
      li.appendChild(a);

      if (level <= 2 || !rootList.lastElementChild) {
        rootList.appendChild(li);
        currentSub = null;
      } else {
        if (!currentSub) {
          currentSub = document.createElement('ul');
          rootList.lastElementChild.appendChild(currentSub);
        }
        currentSub.appendChild(li);
      }
    });

    scroller.appendChild(rootList);

    var nav = document.createElement('nav');
    nav.setAttribute('aria-label', CFG.toc.title || '目次');

    // 広い画面では常に開いたサイドバー、狭い画面では畳めるアコーディオンにする
    var details = document.createElement('details');
    details.className = 'doc-toc-details';

    var summary = document.createElement('summary');
    summary.className = 'doc-toc-title';
    summary.textContent = CFG.toc.title || '目次';

    details.appendChild(summary);
    details.appendChild(scroller);
    nav.appendChild(details);
    host.appendChild(nav);

    var wide = window.matchMedia('(min-width: 60rem)');
    var syncOpen = function () { details.open = wide.matches; };
    syncOpen();
    if (wide.addEventListener) wide.addEventListener('change', syncOpen);
    else if (wide.addListener) wide.addListener(syncOpen);

    return { host: host, headings: headings, links: $$('a', rootList) };
  }

  function setupScrollSpy(toc) {
    if (!toc || !CFG.toc.scrollSpy) return;

    var linkById = {};
    toc.links.forEach(function (a) {
      linkById[decodeURIComponent(a.getAttribute('href').slice(1))] = a;
    });

    var active = null;
    var ticking = false;

    function update() {
      ticking = false;
      var offset = 100;
      var current = toc.headings[0];

      for (var i = 0; i < toc.headings.length; i += 1) {
        if (toc.headings[i].getBoundingClientRect().top <= offset) {
          current = toc.headings[i];
        } else {
          break;
        }
      }

      // 最下部まで来たら最後の見出しを選択状態にする
      if (window.innerHeight + window.scrollY >= document.body.scrollHeight - 2) {
        current = toc.headings[toc.headings.length - 1];
      }

      var link = current && linkById[current.id];
      if (link === active) return;
      if (active) active.removeAttribute('aria-current');
      if (link) {
        link.setAttribute('aria-current', 'true');
        // サイドバー内で見えるようにスクロール
        var box = toc.host.getBoundingClientRect();
        var lb = link.getBoundingClientRect();
        if (lb.top < box.top || lb.bottom > box.bottom) {
          link.scrollIntoView({ block: 'nearest' });
        }
      }
      active = link || null;
    }

    function onScroll() {
      if (ticking) return;
      ticking = true;
      window.requestAnimationFrame(update);
    }

    window.addEventListener('scroll', onScroll, { passive: true });
    window.addEventListener('resize', onScroll, { passive: true });
    update();
  }

  /* ----------------------------------------------------------------------
     見出しアンカー
     ---------------------------------------------------------------------- */

  function addHeadingAnchors(content) {
    var headings = $$(CFG.anchors.headings, content);
    ensureHeadingIds(headings);
    headings.forEach(function (h) {
      if ($('.heading-anchor', h)) return;
      var a = document.createElement('a');
      a.className = 'heading-anchor';
      a.href = '#' + h.id;
      a.textContent = CFG.anchors.symbol;
      a.setAttribute('aria-label', h.textContent.trim() + ' へのリンク');
      h.insertBefore(a, h.firstChild);
    });
  }

  /* ----------------------------------------------------------------------
     コードのコピーボタン
     ---------------------------------------------------------------------- */

  function addCopyButtons(content) {
    $$('pre > code', content).forEach(function (code) {
      var pre = code.parentElement;
      var host = pre.closest('.code-block');

      if (!host) {
        // .code-block でくるまれていない場合は相対配置のラッパを足す
        host = document.createElement('div');
        host.className = 'doc-copy-wrap';
        pre.parentNode.insertBefore(host, pre);
        host.appendChild(pre);
      }
      if ($('.doc-copy-btn', host)) return;

      var btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'doc-copy-btn';
      btn.textContent = CFG.copyCode.label;
      btn.setAttribute('aria-label', 'コードをコピー');

      btn.addEventListener('click', function () {
        var text = code.innerText;
        var done = function (ok) {
          btn.textContent = ok ? CFG.copyCode.copiedLabel : CFG.copyCode.failedLabel;
          btn.dataset.copied = ok ? 'true' : 'false';
          window.setTimeout(function () {
            btn.textContent = CFG.copyCode.label;
            delete btn.dataset.copied;
          }, 1600);
        };

        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(function () { done(true); },
            function () { done(fallbackCopy(text)); });
        } else {
          done(fallbackCopy(text));
        }
      });

      host.appendChild(btn);
    });
  }

  function fallbackCopy(text) {
    try {
      var ta = document.createElement('textarea');
      ta.value = text;
      ta.setAttribute('readonly', '');
      ta.style.position = 'fixed';
      ta.style.opacity = '0';
      document.body.appendChild(ta);
      ta.select();
      var ok = document.execCommand('copy');
      document.body.removeChild(ta);
      return ok;
    } catch (e) {
      return false;
    }
  }

  /* ----------------------------------------------------------------------
     表の横スクロール化
     ---------------------------------------------------------------------- */

  function wrapTables(content) {
    $$('table', content).forEach(function (table) {
      if (table.closest('.doc-table-scroll')) return;
      var wrap = document.createElement('div');
      wrap.className = 'doc-table-scroll';
      wrap.setAttribute('tabindex', '0');
      wrap.setAttribute('role', 'region');
      var cap = $('caption', table);
      wrap.setAttribute('aria-label', cap ? cap.textContent.trim() : '表');
      table.parentNode.insertBefore(wrap, table);
      wrap.appendChild(table);
    });
  }

  /* ----------------------------------------------------------------------
     Mermaid
     ---------------------------------------------------------------------- */

  function initMermaid(nodes) {
    return loadScript(CFG.mermaid.src).then(function () {
      if (!window.mermaid) throw new Error('mermaid not available');
      var cfg = merge({
        startOnLoad: false,
        securityLevel: 'strict',
        theme: prefersDark() ? 'dark' : 'default',
        fontFamily: getComputedStyle(document.body).fontFamily
      }, CFG.mermaid.config);
      window.mermaid.initialize(cfg);
      return window.mermaid.run({ nodes: nodes });
    }).catch(function (e) {
      warn('Mermaid の描画に失敗しました。ソースをそのまま表示します。', e);
      nodes.forEach(function (n) { n.setAttribute('data-processed', 'failed'); });
    });
  }

  /* ----------------------------------------------------------------------
     数式（KaTeX / MathJax）
     ---------------------------------------------------------------------- */

  var MATH_PATTERN = /(\$\$[\s\S]+?\$\$)|(\\\([\s\S]+?\\\))|(\\\[[\s\S]+?\\\])|(\$[^$\n]+\$)/;

  function hasMath(content) {
    if ($('.math, [data-math]', content)) return true;
    // code / pre の中の $1, $HOME などを数式と誤検出しないよう除外して判定する
    var clone = content.cloneNode(true);
    $$('pre, code, kbd, samp, script, style', clone).forEach(function (el) { el.remove(); });
    return MATH_PATTERN.test(clone.textContent || '');
  }

  function renderMathKatex(content) {
    var k = CFG.math.katex;
    loadStyle(k.css);
    return loadScript(k.js)
      .then(function () { return loadScript(k.autoRender); })
      .then(function () {
        if (!window.renderMathInElement) throw new Error('KaTeX auto-render not available');
        window.renderMathInElement(content, {
          delimiters: k.delimiters,
          ignoredTags: k.ignoredTags,
          throwOnError: false
        });
      })
      .catch(function (e) { warn('KaTeX の読み込みに失敗しました。', e); });
  }

  function renderMathMathjax() {
    window.MathJax = merge({
      tex: {
        inlineMath: [['$', '$'], ['\\(', '\\)']],
        displayMath: [['$$', '$$'], ['\\[', '\\]']]
      },
      options: { skipHtmlTags: ['script', 'noscript', 'style', 'textarea', 'pre', 'code'] }
    }, window.MathJax || {});
    return loadScript(CFG.math.mathjax.js)
      .catch(function (e) { warn('MathJax の読み込みに失敗しました。', e); });
  }

  /* ----------------------------------------------------------------------
     シンタックスハイライト（任意）
     ---------------------------------------------------------------------- */

  function initHighlight(content) {
    return loadScript(CFG.highlight.src).then(function () {
      if (!window.hljs) throw new Error('highlight.js not available');
      $$('pre > code', content).forEach(function (block) {
        if (block.dataset.noHighlight !== undefined) return;
        window.hljs.highlightElement(block);
      });
    }).catch(function (e) { warn('highlight.js の読み込みに失敗しました。', e); });
  }

  /* ----------------------------------------------------------------------
     テーマ切替（任意）
     ---------------------------------------------------------------------- */

  function initThemeToggle() {
    var key = CFG.themeToggle.storageKey;
    var stored = null;
    try { stored = window.localStorage.getItem(key); } catch (e) { /* noop */ }
    if (stored === 'dark' || stored === 'light') {
      document.documentElement.setAttribute('data-theme', stored);
    }

    var btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'doc-theme-toggle';
    btn.textContent = '◐';

    function sync() {
      var dark = prefersDark();
      btn.setAttribute('aria-label', dark ? CFG.themeToggle.lightLabel : CFG.themeToggle.darkLabel);
      btn.title = btn.getAttribute('aria-label');
    }

    btn.addEventListener('click', function () {
      var next = prefersDark() ? 'light' : 'dark';
      document.documentElement.setAttribute('data-theme', next);
      try { window.localStorage.setItem(key, next); } catch (e) { /* noop */ }
      sync();
    });

    sync();
    document.body.appendChild(btn);
  }

  /* ----------------------------------------------------------------------
     起動
     ---------------------------------------------------------------------- */

  function init() {
    var content = $(CFG.content) || document.body;

    if (CFG.anchors.enable) {
      try { addHeadingAnchors(content); } catch (e) { warn('見出しアンカー', e); }
    }
    if (CFG.toc.enable) {
      try { setupScrollSpy(buildToc(content)); } catch (e) { warn('目次', e); }
    }
    if (CFG.tableScroll.enable) {
      try { wrapTables(content); } catch (e) { warn('表のラップ', e); }
    }
    if (CFG.copyCode.enable) {
      try { addCopyButtons(content); } catch (e) { warn('コピーボタン', e); }
    }
    if (CFG.themeToggle.enable) {
      try { initThemeToggle(); } catch (e) { warn('テーマ切替', e); }
    }

    var mermaidNodes = $$(CFG.mermaid.selector, content);
    if (enabled(CFG.mermaid.enable, mermaidNodes.length)) {
      initMermaid(mermaidNodes);
    }

    if (enabled(CFG.math.enable, hasMath(content))) {
      if (CFG.math.engine === 'mathjax') renderMathMathjax();
      else renderMathKatex(content);
    }

    if (CFG.highlight.enable) {
      initHighlight(content);
    }

    document.documentElement.setAttribute('data-doc-ready', 'true');
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }

  // 動的にコンテンツを差し替えたときに再実行できるよう公開しておく
  window.DocTemplate = { init: init, config: CFG };
})();
