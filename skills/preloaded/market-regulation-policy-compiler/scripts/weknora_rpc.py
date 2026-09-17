#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""WeKnora MCP gateway RPC helper — reuse for single-file wiki build.
环境信息（网关地址/API Key/知识库 id）不写死在脚本里：
  - 从技能根目录 config.yaml 读取（相对路径，技能自包含；复制 config.example.yaml 为 config.yaml）
    回退：Hermes 本机 config.yaml 的 mcp_servers.weknora（mcp_servers 结构兼容）
  - 知识库: wr.load_kb() 自动读取 config 的 weknora.kb_id；或调用时显式传 load_kb('<uuid>')

Usage:
    import weknora_rpc as wr
    wr.load_config()          # 自动定位技能 config.yaml 读取 URL/KEY（rpc 首次调用会自动触发）
    KB = wr.load_kb()         # 读取知识库 id（或 wr.KB = '<kb_id>' 手动设置）
    wr.mcp_init()
    res = wr.tool_call('list_wiki_pages', {"kb_id": wr.KB, "page": 1, "page_size": 100})
    data = wr.parse_text(res['content'][0]['text'])   # Python repr -> dict
"""
import json
import os
import re
import time
import ast
import urllib.request
import urllib.error

KEY = None
URL = None
KB = None  # 调用前设置：wr.KB = '<目标知识库 id>'；或从技能 config.yaml 的 weknora.kb_id 自动读取

_ID = [0]
SESSION = {'id': None}


def _env_key(name):
    """环境变量 -> 回退 ~/.hermes/.env 文件读取"""
    v = os.environ.get(name, '')
    if v:
        return v
    try:
        for line in open(os.path.expanduser('~/.hermes/.env')):
            line = line.strip()
            if line.startswith(name + '='):
                return line.split('=', 1)[1]
    except Exception:
        pass
    return ''


def _skill_config():
    """技能公共配置：技能根 config.yaml（优先）→ ~/.hermes/config.yaml。返回完整 dict。"""
    import yaml
    skill_cfg = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), 'config.yaml')
    for path in [skill_cfg, os.path.expanduser('~/.hermes/config.yaml')]:
        try:
            cfg = yaml.safe_load(open(path, encoding='utf-8')) or {}
            if cfg.get('model'):
                return cfg
        except Exception:
            continue
    return {}


def _default_model():
    """模型名优先级：任务级 WEKNORA_LLM_MODEL（WeKnora 按知识库绑定模型注入）
    > Agent 主进程环境变量 LLM_MODEL（agent-gateway 容器 compose 注入）
    > 技能 config.yaml model.default > 内置默认。"""
    for env in ('WEKNORA_LLM_MODEL', 'LLM_MODEL'):
        v = os.environ.get(env, '').strip()
        if v:
            return v
    m = _skill_config().get('model', {}).get('default', '') or ''
    return m if m else 'deepseek-v4-flash'


DEFAULT_MODEL = _default_model()  # 全技能 LLM 统一默认模型（配置源：技能 config.yaml model.default）


def llm_config():
    """LLM 统一配置来源：返回 (base_url, api_key)。
    所有脚本一律通过 wr.llm_config() 获取，禁止各自读 config。
    配置优先级：任务级环境变量（WEKNORA_LLM_BASE_URL/WEKNORA_LLM_API_KEY，
    WeKnora 按知识库绑定模型注入）> Agent 主进程环境变量（LLM_BASE_URL/LLM_API_KEY，
    agent-gateway 容器 compose 注入）> 技能 config.yaml model 段 > 内置默认。"""
    import yaml
    _llm2 = _skill_config().get('model', {})
    # 任务级 LLM 配置优先（WeKnora 按知识库绑定模型随任务传入）
    env_base = os.environ.get('WEKNORA_LLM_BASE_URL', '').strip() or os.environ.get('LLM_BASE_URL', '').strip()
    env_key = os.environ.get('WEKNORA_LLM_API_KEY', '').strip() or os.environ.get('LLM_API_KEY', '').strip()
    base_url = (env_base or _llm2.get('base_url') or 'https://api.deepseek.com/v1').rstrip('/')
    api_key = env_key or os.path.expandvars(_llm2.get('api_key', '') or '').strip()
    # ${VAR} 未展开（env 无该变量）视为无效，回退 .env / 环境变量链
    if not api_key or '${' in api_key:
        api_key = _env_key('DEEPSEEK_API_KEY') or os.environ.get('CUSTOM_API_KEY') or os.environ.get('HERMES_CUSTOM_AIAAA_CC_API_KEY') or ''
    # base_url 信任 config.yaml（仅空值/未展开时兜底官方端点）
    if not base_url or '${' in base_url:
        base_url = 'https://api.deepseek.com/v1'
    return base_url, api_key


def _skill_dir():
    """技能 scripts/ 目录（本文件所在目录）。"""
    return os.path.dirname(os.path.abspath(__file__))


def _candidate_configs(config_path=None):
    """候选配置文件列表（按优先级）：
    1. 显式传入的 config_path
    2. Hermes config.yaml（优先，通常含可直接用的 MCP key）
    3. 技能根目录 config.yaml
    4. 脚本同目录 config.yaml
    5. 回退：Windows 默认 Hermes 配置
    """
    if config_path:
        return [config_path]
    home = os.path.expanduser('~')
    skill_root = os.path.dirname(_skill_dir())   # scripts/ 的父目录 = 技能根
    return [
        os.path.join(home, '.hermes', 'config.yaml'),
        os.path.join(skill_root, 'config.yaml'),
        os.path.join(_skill_dir(), 'config.yaml'),
        os.path.join(home, 'AppData', 'Local', 'hermes', 'config.yaml'),
    ]


def _gateway_managed_mcp():
    """平台托管 MCP 配置兜底（2026-09-17）。

    从 agent-gateway 的托管配置（WeKnora「MCP 管理」同步下发，路径取 env
    MCP_MANAGED_PATH，默认 /srv/gateway/data/mcp_servers.managed.json，其次
    MCP_CONFIG_PATH / mcp_servers.json）中解析 `weknora` 服务的 url 与鉴权 key。
    这样技能脚本不再依赖自身 config.yaml，换环境只需在 WeKnora 侧同步一次 MCP 配置。
    取不到返回 ('', '')，交由上层按原逻辑报错。
    """
    import json as _json
    cands = []
    for env_name in ('MCP_MANAGED_PATH', 'MCP_CONFIG_PATH'):
        p = os.environ.get(env_name, '').strip()
        if p:
            cands.append(p)
    cands += [
        '/srv/gateway/data/mcp_servers.managed.json',
        '/srv/gateway/data/mcp_servers.json',
        '/srv/gateway/mcp_servers.json',
    ]
    seen = set()
    for path in cands:
        if path in seen or not os.path.exists(path):
            continue
        seen.add(path)
        try:
            with open(path, encoding='utf-8') as f:
                data = _json.load(f)
        except Exception:
            continue
        items = data if isinstance(data, list) else (
            (data or {}).get('servers') or (data or {}).get('mcp_servers') or [])
        for it in items:
            if not isinstance(it, dict):
                continue
            if (it.get('name') or '').strip().lower() not in ('wiki-tools', 'weknora'):
                continue
            url = (it.get('url') or it.get('endpoint') or '').strip()
            headers = it.get('headers') or it.get('env') or {}
            key = ''
            if isinstance(headers, dict):
                for hk in ('X-API-Key', 'x-api-key', 'Authorization', 'authorization'):
                    v = headers.get(hk)
                    if v:
                        key = str(v).replace('Bearer ', '').strip()
                        break
            if url and key:
                return url, key
    return '', ''


def load_config(config_path=None):
    """读取 WeKnora MCP 配置（URL / API Key / kb_id）。

    配置来源（相对路径优先，技能自包含）：
      - 技能根目录 config.yaml（推荐，结构见 config.example.yaml）
      - 回退 Hermes config.yaml 的 mcp_servers.weknora（本机兼容）

    技能 config.yaml 结构：
      weknora:
        url: http://<host>:8001/mcp/
        api_key: <X-API-Key>
        kb_id: <知识库 UUID>
    """
    global KEY, URL, KB
    errors = []
    for path in _candidate_configs(config_path):
        if not os.path.exists(path):
            continue
        try:
            import yaml
            with open(path, encoding='utf-8') as f:
                cfg = yaml.safe_load(f) or {}
            if 'weknora' in cfg:
                wk = cfg['weknora']
                if not URL:
                    URL = (wk.get('url') or wk.get('endpoint') or '').strip()
                if not KEY:
                    raw_key = wk.get('api_key') or wk.get('apiKey') or ''
                    KEY = os.path.expandvars(raw_key.strip().strip('"').strip("'"))
                if wk.get('kb_id'):
                    KB = wk['kb_id']
            else:
                wk = cfg.get('mcp_servers', {}).get('weknora', {})
                if not URL:
                    URL = (wk.get('url') or wk.get('endpoint') or '').strip()
                if not KEY:
                    headers = wk.get('headers') or {}
                    raw_key = headers.get('X-API-Key', '')
                    KEY = os.path.expandvars(raw_key.strip().strip('"').strip("'"))
            # 不提前 return：Hermes config 有 mcp_servers.weknora 时 URL/KEY 已就绪
            # 但 kb_id 在技能 config.yaml（排在后面）——必须遍历完补全 KB（2026-08-28 修复）。
            # URL/KEY 只取第一个非空（Hermes 网关 key 优先），技能 config 仅补 kb_id，不覆盖 key。
            if URL and not URL.endswith('/'):
                URL = URL + '/'   # MCP 网关必须带尾斜杠，否则 307
        except Exception as e:
            errors.append(f'{path}: {e}')
            continue
    # 多知识库动态触发：agent-gateway 注入的 WEKNORA_KB_ID 优先于 config.yaml 固定值
    env_kb = os.environ.get('WEKNORA_KB_ID', '').strip()
    if env_kb:
        KB = env_kb
    # 平台托管兜底（2026-09-17）：agent-gateway 托管的 MCP 配置（WeKnora「MCP 管理」同步下发）
    # 优先于报错；技能因此无需自带 config.yaml，换环境只需在 WeKnora 侧同步一次。
    if not (URL and KEY):
        g_url, g_key = _gateway_managed_mcp()
        if g_url and not URL:
            URL = g_url
        if g_key and not KEY:
            KEY = g_key
    if URL and not URL.endswith('/'):
        URL = URL + '/'
    if URL and KEY:
        return
    raise RuntimeError(
        '无法读取 weknora 配置。请创建技能根目录 config.yaml（参照 config.example.yaml）'
        f' 或检查 Hermes config.yaml。已尝试: {_candidate_configs(config_path)}；错误: {errors}')


def load_kb(kb_id=None):
    """返回知识库 id：显式传参 > 环境变量 WEKNORA_KB_ID（agent 任务动态注入，多库触发用）
    > 技能 config.yaml 的 weknora.kb_id > 报错。"""
    global KB
    if kb_id:
        KB = kb_id
    # agent-gateway 任务级上下文：run_task 把 config.kb_id 注入 WEKNORA_KB_ID 环境变量
    env_kb = os.environ.get('WEKNORA_KB_ID', '').strip()
    if env_kb and (KB is None or env_kb != (KB if KB else '')):
        KB = env_kb
    if KB is None:
        load_config()
    if KB is None:
        raise RuntimeError('未配置知识库 id：请在技能 config.yaml 设置 weknora.kb_id，或调用 load_kb("<uuid>")')
    return KB


def rpc(method, params, retries=5):
    if URL is None or KEY is None:
        load_config()
    if KB is None and method == 'tools/call':
        # 工具调用需要 kb_id 时由调用方显式传入 arguments，这里不自动填充
        pass
    _ID[0] += 1
    payload = {"jsonrpc": "2.0", "id": _ID[0], "method": method, "params": params}
    req = urllib.request.Request(URL, data=json.dumps(payload).encode(), method='POST')
    req.add_header('Content-Type', 'application/json')
    req.add_header('Accept', 'application/json, text/event-stream')
    req.add_header('X-API-Key', KEY)
    if SESSION['id']:
        req.add_header('Mcp-Session-Id', SESSION['id'])
    for attempt in range(retries):
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                if not SESSION['id'] and resp.headers.get('Mcp-Session-Id'):
                    SESSION['id'] = resp.headers['Mcp-Session-Id']
                body = resp.read().decode()
            for line in body.splitlines():
                if line.startswith('data:'):
                    msg = json.loads(line[5:].strip())
                    if 'result' in msg:
                        return msg['result']
                    if 'error' in msg:
                        raise RuntimeError(f"MCP error: {msg['error']}")
            raise RuntimeError('no data line in response')
        except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError,
                ConnectionResetError, ConnectionAbortedError, OSError) as e:
            if attempt == retries - 1:
                raise
            time.sleep(2 * (attempt + 1))


def mcp_init():
    rpc('initialize', {"protocolVersion": "2024-11-05", "capabilities": {},
                       "clientInfo": {"name": "hermes-single-build", "version": "1.0"}})


def parse_result(res):
    """MCP result is '{"result": "<python-dict-as-string>"}' -> unwrap once."""
    if isinstance(res, dict) and 'result' in res and isinstance(res['result'], str):
        return json.loads(res['result'])
    return res


def parse_text(data):
    """MCP text payload is Python repr (single quotes) — NOT JSON."""
    return ast.literal_eval(data)


def tool_text(res):
    txt = res.get('content', [{}])[0].get('text', '{}')
    try:
        return parse_text(txt)
    except Exception:
        return {'error': txt}


def tool_call(name, arguments):
    """Call any weknora MCP tool. Returns parsed dict.
    ALWAYS check isError + success — absence of exception is NOT success."""
    res = parse_result(rpc('tools/call', {"name": name, "arguments": arguments}))
    if res.get('isError'):
        return {'error': tool_text(res)}
    return tool_text(res)


def _kb():
    if KB is None:
        raise RuntimeError('请先设置 wr.KB = <目标知识库 id>')
    return KB


_CHAT_TAIL_LINE_RE = re.compile(r'^\s*(?:😊[^\n]*|有什么(?:我可以|需要我)帮[^\n]*)\s*$', re.MULTILINE)


def clean_llm_tail(text):
    """清理 LLM 自由文本输出的尾部聊天残留（2026-08-28 实测 33/41 摘要页含
    「😊 有什么我可以帮你的吗？」等问候——LLM 生成摘要卡片后在末尾追加聊天式结尾，
    prompt 要求"只输出 markdown"也拦不住）。只删除文尾的独立问候行（整行匹配，
    正文中出现的相同短语不动）。用法：所有 LLM 生成自由文本（摘要卡片等）落库前必须调用。"""
    if not text:
        return text
    matches = list(_CHAT_TAIL_LINE_RE.finditer(text))
    if matches:
        last = matches[-1]
        # 仅当问候行位于文尾（其后无非空白内容）才删除
        if not text[last.end():].strip():
            text = text[:last.start()].rstrip() + '\n'
    return text


def bookname(name):
    """文件名/制度名 → 展示用书名号包装（2026-08-31 新增，防书名号嵌套）。

    问题：通知/修订类文件名本身含《》（如「关于修订《XXX》的通知.doc」），
    旧代码一律包一层《》→「来源制度」/「原文关联」分组标题出现
    《关于修订《XXX》的通知.doc》嵌套重复（用户实测 106 页来源制度行 + 137 页
    原文关联标题中招）。

    规则：文件名已含《》→ 原样返回（不再包外层）；不含 → 包一层《》。
    所有实体页/规则页/关键词页生成脚本的 `《{file}》` 拼接一律改走本函数。
    """
    name = (name or '').strip()
    if not name:
        return ''
    return name if ('《' in name and '》' in name) else f'《{name}》'


def rewrite_source_line(content, refs):
    """重写 entity 页基本信息表「来源制度」行为来源文档并集（2026-09-02 修复，坑 105）。

    问题：创建即合并（merge_page）只追加 folder_ids/source_refs/原文关联分组/关键规则，
    漏维护基本信息表「来源制度」行——跨文档合并后行停留在首创建者文档，而 source_refs
    与原文关联分组已是全量（数据全、展示漏）。本函数按 refs 并集重写该行。

    refs: [kid|文件名, ...] 或 [文件名, ...]（与 source_refs 同格式）。
    返回新 content；无「来源制度」行/无 refs/无变化 → 原样返回。幂等（已是全量则无变化）。
    """
    if not refs or '来源制度' not in (content or ''):
        return content
    names = []
    for r in refs:
        fname = r.split('|', 1)[1] if '|' in r else r
        n = bookname(fname)
        if n and n not in names:
            names.append(n)
    if not names:
        return content
    joined = '；'.join(names)
    # 只改 ## 基本信息 节内的来源制度行（首个 ## 节），不碰原文关联分组等其他位置
    m = re.search(r'## 基本信息(.*?)(\n## |\Z)', content, re.S)
    if not m:
        return content
    seg = m.group(1)
    new_seg = re.sub(r'^\| 来源制度 \|.*\|$', f'| 来源制度 | {joined} |', seg, count=1, flags=re.M)
    if new_seg == seg:
        return content
    return content[:m.start(1)] + new_seg + content[m.end(1):]


# ---- 常用工具快捷函数（kb 用模块级 KB，或显式传参覆盖）----
def list_docs(kb_id=None, page=1, page_size=100):
    return tool_call('list_documents', {"kb_id": kb_id or _kb(), "page": page, "page_size": page_size})


def list_chunks(kid, kb_id=None, page=1, page_size=100):
    return tool_call('list_chunks', {"kb_id": kb_id or _kb(), "knowledge_id": kid,
                                     "page": page, "page_size": page_size})


def list_folders(kb_id=None, parent_id=''):
    return tool_call('list_wiki_folders', {"kb_id": kb_id or _kb(), "parent_id": parent_id})


def list_wiki_pages(kb_id=None, page=1, page_size=200):
    """分页拉取 wiki 页面。注意：wiki_search 只返回前 N 条（约50），
    全量扫描必须用 list_wiki_pages 分页！"""
    return tool_call('list_wiki_pages', {"kb_id": kb_id or _kb(), "page": page, "page_size": page_size})


def wiki_search(query, kb_id=None, limit=100):
    """搜索 wiki 页面。注意：上限约 50 条，不能替代 list_wiki_pages 全量扫描。"""
    return tool_call('wiki_search', {"kb_id": kb_id or _kb(), "query": query, "limit": limit})


def walk_folders(kb_id=None, parent_id='', collect=None):
    """递归遍历目录树（MCP 可能漏子目录，逐层展开交叉核对）。
    collect: dict 用于收集 {folder_id: name}，默认内部累积。
    注意：全库遍历较慢（数百目录可能耗时 1 分钟+），验证时优先从
    目标文件目录 id 作为 parent_id 局部遍历。"""
    if collect is None:
        collect = {}
    r = list_folders(kb_id=kb_id, parent_id=parent_id)
    for f in r.get('folders', []):
        collect[f['id']] = f['name']
        walk_folders(kb_id=kb_id, parent_id=f['id'], collect=collect)
    return collect


def create_folder(name, parent_id='', kb_id=None):
    """建目录。注意参数顺序：parent_id 在第二位（kb_id 用关键字或模块 KB）。"""
    return tool_call('create_wiki_folder', {"kb_id": kb_id or _kb(), "name": name, "parent_id": parent_id})


def update_folder(folder_id, kb_id=None, name=None, parent_id=None):
    """更新目录（重命名/重挂父目录）。坑 43：改名/移动后须同步 DB 修正 wiki_folders.path。
    注意：MCP update_wiki_folder 的 parent_id 必须配 move_parent=True 才生效（2026-08-28 实测）。"""
    args = {"kb_id": kb_id or _kb(), "folder_id": folder_id}
    if name is not None:
        args['name'] = name
    if parent_id is not None:
        args['parent_id'] = parent_id
        args['move_parent'] = True
    return tool_call('update_wiki_folder', args)


def delete_folder(folder_id, kb_id=None):
    return tool_call('delete_wiki_folder', {"kb_id": kb_id or _kb(), "folder_id": folder_id})


def move_page(slug, kb_id=None, folder_id=''):
    return tool_call('move_wiki_page', {"kb_id": kb_id or _kb(), "slug": slug, "folder_id": folder_id})


def create_page(slug, title, content, folder_id, kb_id=None, page_type=None, summary='', source_refs=None):
    """Create a wiki page. page_type is REQUIRED — pass 'business_ontology', 'rule_ontology', 'summary', 'original_sentence', or 'concept'."""
    if page_type is None:
        raise ValueError("page_type is required for create_page. Use 'business_ontology' for entity-b, 'rule_ontology' for entity-r.")
    args = {"kb_id": kb_id or _kb(), "slug": slug, "title": title, "content": content,
            "folder_id": folder_id, "page_type": page_type, "summary": summary}
    if source_refs:
        args['source_refs'] = source_refs
    return tool_call('create_wiki_page', args)


def update_page(slug, kb_id=None, content=None, folder_id=None, summary=None,
                source_refs=None, page_type=None, aliases=None,
                title=None, status=None, folder_ids=None):
    """CRITICAL: always pass folder_id — omitting it RESETS the page to root!

    2026-08-28 扩展：补齐 title/status/folder_ids 透传（MCP update_wiki_page 支持）。
    全技能 20+ 脚本曾因传 title/status 报 TypeError（如 extract_entities_single.py
    create_page 补写 source_refs 时带 title/status）——坑 1 要求回读原值带全字段，
    快捷函数必须收下这些字段转发给 MCP，而不是拒绝。"""
    args = {"kb_id": kb_id or _kb(), "slug": slug}
    if content is not None:
        args['content'] = content
    if folder_id is not None:
        args['folder_id'] = folder_id
    if summary is not None:
        args['summary'] = summary
    if source_refs is not None:
        args['source_refs'] = source_refs
    if page_type is not None:
        args['page_type'] = page_type
    if aliases is not None:
        args['aliases'] = aliases
    if title is not None:
        args['title'] = title
    if status is not None:
        args['status'] = status
    if folder_ids is not None:
        args['folder_ids'] = folder_ids
    return tool_call('update_wiki_page', args)


def read_page(slug, kb_id=None):
    # 2026-09-08 兜底：网关对不存在页面偶发返回 HTTP 404（非惯例的 HTTP 200+body error），
    # rpc() 重试 5 次后 raise HTTPError → 全技能幂等查重（'error' not in r → 建页）崩溃，
    # 实跑致化妆品篇整篇实体=0。404 统一转 {'error': ...} 维持「不存在」语义（坑 110）。
    try:
        return tool_call('wiki_read_page', {"kb_id": kb_id or _kb(), "slug": slug})
    except urllib.error.HTTPError as e:
        if e.code == 404:
            return {'error': f'404 page not found: {slug}'}
        raise


def delete_page(slug, kb_id=None):
    return tool_call('delete_wiki_page', {"kb_id": kb_id or _kb(), "slug": slug})


def rebuild_links(kb_id=None):
    return tool_call('wiki_rebuild_links', {"kb_id": kb_id or _kb()})


def log_progress(action, knowledge_id='', doc_title='', summary='', kb_id=None, page_slugs=None):
    """向 WeKnora 知识库写入一条 wiki 处理进度日志（agent 执行过程中按大步骤上报）。

    底层调用 MCP wiki_log_write（需 API key 具备 write 能力）。失败仅告警不抛出，
    避免进度上报问题阻断 wiki 构建主流程。

    action 建议取值：agent_build_start / agent_build_relations / agent_build_dirs /
    agent_build_summary / agent_build_entities / agent_build_keywords /
    agent_build_index / agent_build_done。
    """
    args = {"kb_id": kb_id or _kb(), "action": action}
    if knowledge_id:
        args['knowledge_id'] = knowledge_id
    if doc_title:
        args['doc_title'] = doc_title
    if summary:
        args['summary'] = summary
    if page_slugs:
        args['page_slugs'] = page_slugs
    try:
        return tool_call('wiki_log_write', args)
    except Exception as e:
        print(f'  ⚠️ wiki_log_write 失败(不阻断): {action} -> {e}')
        return {'error': str(e)}


def find_file(file_name, kb_id=None):
    """按 file_name 找文件，返回 doc dict 或 None（支持精确/包含匹配）。"""
    kb = kb_id or _kb()
    page = 1
    while True:
        r = list_docs(kb_id=kb, page=page)
        data = r.get('data') or []
        for d in data:
            if d.get('file_name') == file_name:
                return d
        if len(data) < 100:
            break
        page += 1
    page = 1
    while True:
        r = list_docs(kb_id=kb, page=page)
        data = r.get('data') or []
        for d in data:
            if file_name in (d.get('file_name') or ''):
                return d
        if len(data) < 100:
            break
        page += 1
    return None


def fetch_all_chunks(kid, kb_id=None):
    """分页拉全切片，按 chunk_index 排序，返回 [{'content','chunk_index'}, ...]"""
    chunks, page = [], 1
    while True:
        r = list_chunks(kid, kb_id=kb_id, page=page)
        data = r.get('data') or []
        chunks.extend([{'content': c.get('content', ''), 'chunk_index': c.get('chunk_index', 0)}
                       for c in data])
        if len(data) < 100:
            break
        page += 1
    chunks.sort(key=lambda c: c['chunk_index'])
    return chunks


def clean_chunk_text(text):
    """清洗切片：去 markdown 锚点链接、图片 resource 标记、纯页码行与 markdown 标题标记（#/###）。
    PDF 页码线（2026-09-07 市场库实测：黄河保护法 PDF 每页页脚「— ８４３ —」/「- 843 -」全会话成行）"""
    import re
    text = re.sub(r'\[([^\]]*)\]\(#[^)]*\)', r'\1', text)  # [..](#_Toc..) -> ..
    # doc 转 markdown 图片标记（2026-08-31：doc 解析器把图片转成 ![..](resource://..)，
    # 残留会①被切句成「![](resource://」噪音长句页 ②从中间截断正文（「框架协![..]议订单管理细则」），
    # 直接整段删除——resource:// 是内部资源引用非正文）
    text = re.sub(r'!\[[^\]]*\]\(resource://[^)]*\)', '', text)
    # PDF 页码线（2026-09-07）：法规 PDF（黄河保护法/反间谍法）页脚「— ８４３ —」，行级删除
    text = re.sub(r'^\s*[-—－]\s*[\d０-９0-9\s]{1,6}\s*[-—－]\s*$', '', text, flags=re.MULTILINE)
    # markdown 标题标记（2026-08-28：docx 转 markdown 的 # 残留，行首去标记保留文字，中间序列删除）
    text = re.sub(r'^#{1,6}\s*', '', text, flags=re.MULTILINE)
    text = re.sub(r'#{2,6}', '', text)
    lines = [ln for ln in text.splitlines() if ln.strip() and not re.fullmatch(r'\[\d+\]', ln.strip())]
    return '\n'.join(lines)


# ---- LLM 调用工具（2026-08-19 新增，统一重试+截断处理）----

def tokenize(text, kb_id=None):
    """分词工具快捷函数。"""
    return tool_call('tokenize', {"kb_id": kb_id or _kb(), "text": text})


def llm_call(messages, model=None, max_tokens=4096, temperature=0.1,
             retries=5, base_delay=3, timeout=300):
    """调用 LLM API 的通用函数，带重试和 JSON 截断处理。

    参数:
        messages: [{"role":"user","content":"..."}]
        model: 模型名，默认 DEFAULT_MODEL
        max_tokens: 最大输出 token，默认 4096
        temperature: 温度，默认 0.1
        retries: 最大重试次数，默认 5
        base_delay: 初始退避秒数，默认 3
    返回:
        content: LLM 输出的文本字符串
    异常:
        RuntimeError: 所有重试均失败后抛出

    注意:
        - 自动处理 403 (rate limit) 退避重试
        - 自动检测 finish_reason=length 并打印警告
        - 使用 enable_thinking=false 避免 reasoning 耗尽 max_tokens
    """
    import yaml, urllib.request, time, json
    base_url, api_key = llm_config()
    model = model or DEFAULT_MODEL

    url = f"{base_url}/chat/completions"
    payload = {
        "model": model,
        "messages": messages,
        "max_tokens": max_tokens,
        "temperature": temperature,
        "enable_thinking": False,
    }
    data = json.dumps(payload).encode()
    req = urllib.request.Request(url, data, {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {api_key}"
    })

    last_err = None
    for attempt in range(retries):
        try:
            resp = json.loads(urllib.request.urlopen(req, timeout=timeout).read().decode())
            choice = resp['choices'][0]
            content = (choice['message'].get('content') or '').strip()
            finish = choice.get('finish_reason', '')

            if finish == 'length':
                print(f'  ⚠️ LLM 输出被截断 (finish_reason=length, {len(content)} chars)')

            return content

        except urllib.error.HTTPError as e:
            code = e.code
            if code == 403:
                delay = min(base_delay * (2 ** attempt), 60)
                print(f'  ⏳ LLM 403 (rate limit), 等待 {delay}s 重试 ({attempt+1}/{retries})')
                time.sleep(delay)
                last_err = e
                continue
            elif code == 429:
                delay = min(base_delay * (2 ** attempt), 30)
                print(f'  ⏳ LLM 429 (rate limit), 等待 {delay}s 重试 ({attempt+1}/{retries})')
                time.sleep(delay)
                last_err = e
                continue
            else:
                raise  # 其他 HTTP 错误直接抛出

        except (urllib.error.URLError, TimeoutError, ConnectionResetError) as e:
            delay = base_delay * (attempt + 1)
            print(f'  ⏳ LLM 网络错误: {e}, 等待 {delay}s 重试 ({attempt+1}/{retries})')
            time.sleep(delay)
            last_err = e
            continue

        except json.JSONDecodeError as e:
            delay = base_delay * (attempt + 1)
            print(f'  ⏳ LLM 响应解析失败: {e}, 等待 {delay}s 重试 ({attempt+1}/{retries})')
            time.sleep(delay)
            last_err = e
            continue

    raise RuntimeError(f'LLM 调用失败，已重试 {retries} 次: {last_err}')


def ensure_full_json(text):
    """尝试修复截断的 JSON 数组（LLM 输出被 max_tokens 截断时使用）。

    策略：
    1. 去掉 markdown 代码块标记
    2. 找到最后一个完整的 JSON 对象
    3. 补全闭合括号
    """
    import re
    text = text.strip()
    # 去 markdown 代码块
    if '```json' in text:
        text = text.split('```json')[1].split('```')[0].strip()
    elif '```' in text:
        text = text.split('```')[1].split('```')[0].strip()

    # 尝试直接解析
    try:
        json.loads(text)
        return text  # 完整有效
    except json.JSONDecodeError:
        pass

    # 找到最后一个完整对象
    depth = 0
    last_complete = 0
    for i, ch in enumerate(text):
        if ch == '{':
            depth += 1
        elif ch == '}':
            depth -= 1
            if depth == 0:
                last_complete = i + 1

    if last_complete > 0:
        candidate = text[:last_complete]
        if not candidate.endswith(']'):
            # 尝试补全数组
            if candidate.rstrip().endswith(','):
                candidate = candidate.rstrip()[:-1]
            candidate += '\n]'
        try:
            json.loads(candidate)
            return candidate
        except json.JSONDecodeError:
            pass

    return text  # 无法修复，返回原文


if __name__ == '__main__':
    load_config()
    mcp_init()
    if KB is None:
        print('请先设置 wr.KB = <目标知识库 id> 再运行')
    else:
        r = tool_call('list_knowledge_bases', {})
        print(str(r)[:200])
