/* diffsyntax.js — vendored unified-diff renderer with minimal syntax highlighting.
 * No CDN, no external deps. Works as a classic browser <script> (exposes DiffSyntax global).
 * Supports: go, js/ts/jsx/tsx, python, json, sh/bash, css, html/xml, yaml, rust, java, rb, md.
 */
(function (root) {
  'use strict';

  // ── HTML escaping (must run before any innerHTML assignment) ──────────────
  function esc(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  // ── Language detection ────────────────────────────────────────────────────
  function langFromPath(p) {
    const ext = (p || '').replace(/.*\./, '').toLowerCase();
    const MAP = {
      go: 'go', js: 'js', jsx: 'js', ts: 'js', tsx: 'js', mjs: 'js', cjs: 'js',
      py: 'py', pyi: 'py',
      json: 'json', jsonc: 'json',
      sh: 'sh', bash: 'sh', zsh: 'sh',
      css: 'css', scss: 'css', less: 'css',
      html: 'html', htm: 'html', xml: 'html', svg: 'html',
      yaml: 'yaml', yml: 'yaml',
      rs: 'rs',
      java: 'java',
      rb: 'rb',
      md: 'md', markdown: 'md',
      sql: 'sql',
      c: 'c', h: 'c', cpp: 'c', cc: 'c', cxx: 'c', cs: 'c',
    };
    return MAP[ext] || 'txt';
  }

  // ── Token patterns per language ───────────────────────────────────────────
  // Each entry: [regex, className]  — matched left-to-right, first match wins.
  const TOKENS = {
    go: [
      [/\/\/[^\n]*/,                                                                           'cmt'],
      [/\/\*[\s\S]*?\*\//,                                                                     'cmt'],
      [/`[^`]*`/,                                                                              'str'],
      [/"(?:[^"\\]|\\.)*"/,                                                                    'str'],
      [/'(?:[^'\\]|\\.)*'/,                                                                    'str'],
      [/\b(break|case|chan|const|continue|default|defer|else|fallthrough|for|func|go|goto|if|import|interface|map|package|range|return|select|struct|switch|type|var|nil|true|false|iota|new|make|len|cap|append|delete|copy|close|panic|recover|error)\b/, 'kw'],
      [/\b(int|int8|int16|int32|int64|uint|uint8|uint16|uint32|uint64|uintptr|float32|float64|complex64|complex128|byte|rune|string|bool|error|any)\b/, 'ty'],
      [/\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?i?\b/,                                                'num'],
    ],
    js: [
      [/\/\/[^\n]*/,                                                                           'cmt'],
      [/\/\*[\s\S]*?\*\//,                                                                     'cmt'],
      [/`(?:[^`\\]|\\.|\$\{[^}]*\})*`/,                                                       'str'],
      [/"(?:[^"\\]|\\.)*"/,                                                                    'str'],
      [/'(?:[^'\\]|\\.)*'/,                                                                    'str'],
      [/\b(async|await|break|case|catch|class|const|continue|debugger|default|delete|do|else|export|extends|finally|for|from|function|if|import|in|instanceof|let|new|of|return|static|super|switch|this|throw|try|typeof|var|void|while|with|yield|null|undefined|true|false)\b/, 'kw'],
      [/\b(Array|Object|Promise|Map|Set|WeakMap|WeakSet|Symbol|BigInt|Number|String|Boolean|Error|TypeError|RangeError|Math|JSON|Date|RegExp|Function|console|window|document|globalThis|setTimeout|clearTimeout|setInterval|clearInterval|fetch|URL|URLSearchParams|FormData|Headers|Request|Response)\b/, 'bi'],
      [/\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?n?\b/,                                                'num'],
    ],
    py: [
      [/#[^\n]*/,                                                                              'cmt'],
      [/"""[\s\S]*?"""|'''[\s\S]*?'''/,                                                        'str'],
      [/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/,                                                  'str'],
      [/\b(and|as|assert|async|await|break|class|continue|def|del|elif|else|except|finally|for|from|global|if|import|in|is|lambda|nonlocal|not|or|pass|raise|return|try|while|with|yield|None|True|False)\b/, 'kw'],
      [/\b(int|float|str|bool|list|dict|tuple|set|bytes|bytearray|type|object|super|property|staticmethod|classmethod|print|len|range|enumerate|zip|map|filter|sorted|reversed|open|input|isinstance|issubclass|hasattr|getattr|setattr|delattr|vars|dir|id|hash|repr|abs|round|min|max|sum|any|all)\b/, 'bi'],
      [/\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/,                                                  'num'],
    ],
    json: [
      [/"(?:[^"\\]|\\.)*"\s*:/,                                                                'key'],
      [/"(?:[^"\\]|\\.)*"/,                                                                    'str'],
      [/\b(true|false|null)\b/,                                                                'kw'],
      [/-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/,                                                  'num'],
    ],
    sh: [
      [/#[^\n]*/,                                                                              'cmt'],
      [/"(?:[^"\\]|\\.)*"|'[^']*'/,                                                            'str'],
      [/\b(if|then|else|elif|fi|for|while|do|done|case|esac|function|in|return|break|continue|exit|export|local|readonly|shift|set|unset|source|eval|exec)\b/, 'kw'],
      [/\$\w+|\$\{[^}]+\}/,                                                                    'bi'],
      [/\b\d+\b/,                                                                              'num'],
    ],
    css: [
      [/\/\*[\s\S]*?\*\//,                                                                     'cmt'],
      [/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/,                                                  'str'],
      [/#[0-9a-fA-F]{3,8}\b/,                                                                  'num'],
      [/\b\d+(?:\.\d+)?(?:px|em|rem|vh|vw|%|s|ms|deg|fr|ch|ex|vmin|vmax|cm|mm|in|pt|pc)?\b/,  'num'],
      [/@[\w-]+/,                                                                              'kw'],
      [/:[:\w-]+(?=\s*[{(])/,                                                                  'bi'],
    ],
    html: [
      [/<!--[\s\S]*?-->/,                                                                      'cmt'],
      [/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/,                                                  'str'],
      [/<\/?[\w:-]+/,                                                                          'kw'],
      [/&[a-z#0-9]+;/,                                                                         'bi'],
    ],
    yaml: [
      [/#[^\n]*/,                                                                              'cmt'],
      [/"(?:[^"\\]|\\.)*"|'[^']*'/,                                                            'str'],
      [/\b(true|false|null|yes|no|on|off)\b/,                                                  'kw'],
      [/^[\s-]*[\w.]+(?=\s*:)/m,                                                               'key'],
      [/-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/,                                                  'num'],
    ],
    rs: [
      [/\/\/[^\n]*/,                                                                           'cmt'],
      [/\/\*[\s\S]*?\*\//,                                                                     'cmt'],
      [/"(?:[^"\\]|\\.)*"/,                                                                    'str'],
      [/'(?:[^'\\]|\\.)*'/,                                                                    'str'],
      [/\b(as|async|await|break|const|continue|crate|dyn|else|enum|extern|false|fn|for|if|impl|in|let|loop|match|mod|move|mut|pub|ref|return|self|Self|static|struct|super|trait|true|type|unsafe|use|where|while|abstract|become|box|do|final|macro|override|priv|try|typeof|unsized|virtual|yield)\b/, 'kw'],
      [/\b(bool|char|f32|f64|i8|i16|i32|i64|i128|isize|str|u8|u16|u32|u64|u128|usize|String|Vec|Option|Result|Box|Rc|Arc|HashMap|BTreeMap|HashSet|BTreeSet)\b/, 'ty'],
      [/\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?(?:_?[iu]\d+|_?f\d+)?\b/,                             'num'],
    ],
    java: [
      [/\/\/[^\n]*/,                                                                           'cmt'],
      [/\/\*[\s\S]*?\*\//,                                                                     'cmt'],
      [/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/,                                                  'str'],
      [/\b(abstract|assert|break|case|catch|class|const|continue|default|do|else|enum|extends|final|finally|for|goto|if|implements|import|instanceof|interface|native|new|package|private|protected|public|return|static|strictfp|super|switch|synchronized|this|throw|throws|transient|try|var|void|volatile|while|null|true|false)\b/, 'kw'],
      [/\b(int|long|short|byte|char|float|double|boolean|String|Object|Integer|Long|Double|Float|Boolean|Character|Byte|Short|List|Map|Set|Array|ArrayList|HashMap)\b/, 'ty'],
      [/\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?[lLfFdD]?\b/,                                         'num'],
    ],
    rb: [
      [/#[^\n]*/,                                                                              'cmt'],
      [/=begin[\s\S]*?=end/,                                                                   'cmt'],
      [/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|%\w?\{[^}]*\}/,                                   'str'],
      [/\b(BEGIN|END|__callee__|__dir__|__method__|alias|and|begin|break|case|class|def|defined\?|do|else|elsif|end|ensure|false|for|if|in|module|next|nil|not|or|redo|rescue|retry|return|self|super|then|true|undef|unless|until|when|while|yield)\b/, 'kw'],
      [/:[a-zA-Z_]\w*/,                                                                        'bi'],
      [/\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b/,                                                  'num'],
    ],
    sql: [
      [/--[^\n]*/,                                                                             'cmt'],
      [/\/\*[\s\S]*?\*\//,                                                                     'cmt'],
      [/'(?:[^'\\]|\\.)*'/,                                                                    'str'],
      [/\b(SELECT|FROM|WHERE|JOIN|LEFT|RIGHT|INNER|OUTER|ON|AS|AND|OR|NOT|IN|IS|NULL|LIKE|ORDER|BY|GROUP|HAVING|LIMIT|OFFSET|INSERT|INTO|VALUES|UPDATE|SET|DELETE|CREATE|TABLE|INDEX|DROP|ALTER|ADD|COLUMN|PRIMARY|KEY|FOREIGN|REFERENCES|UNIQUE|DEFAULT|AUTO_INCREMENT|SERIAL|BOOLEAN|INTEGER|VARCHAR|TEXT|DATE|TIMESTAMP|WITH|UNION|ALL|DISTINCT|COUNT|SUM|AVG|MIN|MAX|CASE|WHEN|THEN|ELSE|END)\b/i, 'kw'],
      [/\b\d+(?:\.\d+)?\b/,                                                                    'num'],
    ],
    c: [
      [/\/\/[^\n]*/,                                                                           'cmt'],
      [/\/\*[\s\S]*?\*\//,                                                                     'cmt'],
      [/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'/,                                                  'str'],
      [/#\w+/,                                                                                  'kw'],
      [/\b(auto|break|case|char|const|continue|default|do|double|else|enum|extern|float|for|goto|if|inline|int|long|register|restrict|return|short|signed|sizeof|static|struct|switch|typedef|union|unsigned|void|volatile|while|nullptr|true|false|null|NULL)\b/, 'kw'],
      [/\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?[uUlLfF]*\b/,                                         'num'],
    ],
    md: [],
    txt: [],
  };

  // ── Tokenize a code fragment into highlighted HTML ────────────────────────
  function highlightCode(code, lang) {
    const patterns = TOKENS[lang] || [];
    if (!patterns.length) return esc(code);

    const CLASSES = { cmt: 'sh-c', str: 'sh-s', kw: 'sh-k', ty: 'sh-t', bi: 'sh-b', num: 'sh-n', key: 'sh-p' };
    let out = '';
    let i = 0;
    while (i < code.length) {
      let best = null, bestIdx = Infinity, bestLen = 0, bestCls = '';
      for (const [rx, cls] of patterns) {
        const re = new RegExp(rx.source, rx.flags.replace(/g/, '') + 'g');
        re.lastIndex = i;
        const m = re.exec(code);
        if (m && m.index < bestIdx) {
          best = m;
          bestIdx = m.index;
          bestLen = m[0].length;
          bestCls = cls;
        }
      }
      if (!best || bestIdx >= code.length) {
        out += esc(code.slice(i));
        break;
      }
      if (bestIdx > i) out += esc(code.slice(i, bestIdx));
      out += `<span class="${CLASSES[bestCls] || ''}">${esc(best[0])}</span>`;
      i = bestIdx + bestLen;
    }
    return out;
  }

  // ── Unified diff parser ───────────────────────────────────────────────────
  // Returns array of hunks; each hunk: { header, lines: [{type,old,new,text}] }
  function parseDiff(raw) {
    const lines = raw.split('\n');
    const hunks = [];
    let hunk = null;
    let oldLine = 0, newLine = 0;

    for (const line of lines) {
      if (line.startsWith('@@')) {
        const m = line.match(/@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)/);
        if (m) {
          hunk = { header: line, lines: [] };
          hunks.push(hunk);
          oldLine = parseInt(m[1], 10);
          newLine = parseInt(m[2], 10);
        }
        continue;
      }
      if (!hunk) continue;
      if (line.startsWith('+')) {
        hunk.lines.push({ type: 'add', old: null, new: newLine++, text: line.slice(1) });
      } else if (line.startsWith('-')) {
        hunk.lines.push({ type: 'del', old: oldLine++, new: null, text: line.slice(1) });
      } else if (line.startsWith(' ') || line === '') {
        hunk.lines.push({ type: 'ctx', old: oldLine++, new: newLine++, text: line.slice(1) });
      }
      // skip \\ No newline at end of file
    }
    return hunks;
  }

  // ── Render diff to a container DOM node ──────────────────────────────────
  // opts: { maxCtxLines: 3 }
  function renderUnifiedDiff(container, rawDiff, filePath, opts) {
    const lang = langFromPath(filePath);
    const maxCtx = (opts && opts.maxCtxLines != null) ? opts.maxCtxLines : 3;
    container.innerHTML = '';

    if (!rawDiff || rawDiff.trim() === '') {
      const empty = document.createElement('div');
      empty.className = 'ds-empty';
      empty.textContent = 'No diff content.';
      container.appendChild(empty);
      return;
    }

    // Collect "file header" lines (before first @@)
    const headerLines = [];
    const body = rawDiff.split('\n');
    let firstHunkIdx = body.findIndex(l => l.startsWith('@@'));
    if (firstHunkIdx === -1) firstHunkIdx = body.length;
    for (let i = 0; i < firstHunkIdx; i++) headerLines.push(body[i]);

    const hunks = parseDiff(rawDiff);

    // Render each hunk
    for (const hunk of hunks) {
      // Hunk separator
      const sep = document.createElement('div');
      sep.className = 'ds-hunk-header';
      sep.textContent = hunk.header;
      container.appendChild(sep);

      // Group context lines for collapsing
      const lines = hunk.lines;
      let i = 0;
      while (i < lines.length) {
        const ln = lines[i];
        // Collect consecutive context lines
        if (ln.type === 'ctx') {
          let j = i;
          while (j < lines.length && lines[j].type === 'ctx') j++;
          const ctxRun = lines.slice(i, j);
          if (ctxRun.length > maxCtx * 2 + 1) {
            // Render first maxCtx, then collapsed, then last maxCtx
            for (let k = 0; k < maxCtx && k < ctxRun.length; k++) {
              container.appendChild(buildLine(ctxRun[k], lang));
            }
            const hidden = ctxRun.slice(maxCtx, ctxRun.length - maxCtx);
            container.appendChild(buildCollapse(hidden, lang));
            for (let k = ctxRun.length - maxCtx; k < ctxRun.length; k++) {
              container.appendChild(buildLine(ctxRun[k], lang));
            }
          } else {
            for (const cl of ctxRun) container.appendChild(buildLine(cl, lang));
          }
          i = j;
        } else {
          container.appendChild(buildLine(ln, lang));
          i++;
        }
      }
    }

    if (hunks.length === 0 && rawDiff.includes('Binary files')) {
      const bin = document.createElement('div');
      bin.className = 'ds-binary';
      bin.textContent = 'Binary file — no textual diff available.';
      container.appendChild(bin);
    }
  }

  function buildLine(ln, lang) {
    const row = document.createElement('div');
    row.className = 'ds-line ds-' + ln.type;
    // Line numbers
    const oldNum = document.createElement('span');
    oldNum.className = 'ds-ln ds-ln-old';
    oldNum.textContent = ln.old != null ? ln.old : '';
    const newNum = document.createElement('span');
    newNum.className = 'ds-ln ds-ln-new';
    newNum.textContent = ln.new != null ? ln.new : '';
    const sign = document.createElement('span');
    sign.className = 'ds-sign';
    sign.textContent = ln.type === 'add' ? '+' : ln.type === 'del' ? '-' : ' ';
    const code = document.createElement('span');
    code.className = 'ds-code';
    code.innerHTML = highlightCode(ln.text, lang);
    row.appendChild(oldNum);
    row.appendChild(newNum);
    row.appendChild(sign);
    row.appendChild(code);
    return row;
  }

  function buildCollapse(hidden, lang) {
    const wrap = document.createElement('div');
    wrap.className = 'ds-collapse';
    const btn = document.createElement('button');
    btn.className = 'ds-expand-btn';
    btn.textContent = `⋯ ${hidden.length} unchanged line${hidden.length === 1 ? '' : 's'} — click to show`;
    btn.addEventListener('click', () => {
      const frag = document.createDocumentFragment();
      for (const cl of hidden) frag.appendChild(buildLine(cl, lang));
      wrap.replaceWith(frag);
    });
    wrap.appendChild(btn);
    return wrap;
  }

  root.DiffSyntax = { renderUnifiedDiff, escapeHtml: esc, langFromPath };
})(typeof globalThis !== 'undefined' ? globalThis : this);
