#!/usr/bin/env python3
"""
MCP Gateway SSE 连接测试

测试步骤：
  1. 连接 SSE 端点，获取 message endpoint
  2. 发送 initialize 请求（JSON-RPC）
  3. 发送 tools/list 请求
  4. 列举可用工具
  5. 尝试调用一个工具

用法：
  python3 test_mcp_sse.py
"""

import json
import sys
import time
import uuid
import requests

# ── 配置 ──────────────────────────────────────────────────────────
SSE_URL = "http://10.19.196.20:8086/mcp/679dae45-2b7d-460a-8c25-486e445d61f0/sse"
AUTH_TOKEN = "dev-mcp-gateway-token"
REQUEST_TIMEOUT = 30  # SSE 读取超时（秒）

# ── 工具函数 ──────────────────────────────────────────────────────

def make_jsonrpc_request(method: str, params: dict = None) -> dict:
    """构造 JSON-RPC 2.0 请求"""
    return {
        "jsonrpc": "2.0",
        "id": str(uuid.uuid4()),
        "method": method,
        "params": params or {},
    }


def log_step(step: str, detail: str = ""):
    """格式化输出步骤信息"""
    print(f"\n{'='*60}")
    print(f"  [{step}] {detail}")
    print(f"{'='*60}")


def log_success(msg: str):
    print(f"  ✓ {msg}")


def log_error(msg: str):
    print(f"  ✗ {msg}")


def log_info(msg: str):
    print(f"  ℹ {msg}")


# ── 主测试流程 ──────────────────────────────────────────────────

def main():
    failed = False
    headers = {
        "Authorization": f"Bearer {AUTH_TOKEN}",
        "Accept": "text/event-stream",
        "Cache-Control": "no-cache",
    }

    # ─── Step 1: 连接 SSE ──────────────────────────────────────
    log_step("1/5", "连接 SSE 端点")
    log_info(f"SSE URL: {SSE_URL}")

    try:
        resp = requests.get(
            SSE_URL,
            headers=headers,
            stream=True,
            timeout=(10, REQUEST_TIMEOUT),
        )
        log_success(f"HTTP 状态码: {resp.status_code}")
        if resp.status_code != 200:
            log_error(f"SSE 连接失败: HTTP {resp.status_code}")
            log_info(f"响应内容: {resp.text[:500]}")
            return 1
    except requests.exceptions.ConnectTimeout:
        log_error("连接超时 — 目标地址不可达")
        return 1
    except requests.exceptions.ConnectionError as e:
        log_error(f"连接被拒绝: {e}")
        return 1
    except Exception as e:
        log_error(f"SSE 连接异常: {e}")
        return 1

    # ─── Step 2: 解析 SSE，获取 message endpoint ──────────────
    log_step("2/5", "解析 SSE 事件，获取 message endpoint")

    message_endpoint = None
    sse_events = []
    iter_count = 0
    start_time = time.time()

    try:
        for raw_line in resp.iter_lines():
            if not raw_line:
                continue

            if isinstance(raw_line, bytes):
                raw_line = raw_line.decode("utf-8")

            # SSE 格式: "event: <type>" 或 "data: <payload>"
            if raw_line.startswith("event: "):
                event_type = raw_line[7:]
                sse_events.append({"event": event_type, "data": ""})
            elif raw_line.startswith("data: "):
                data_str = raw_line[6:]
                if not sse_events:
                    sse_events.append({"event": "message", "data": ""})
                sse_events[-1]["data"] = data_str

                # 检查是否有 endpoint 事件
                if sse_events[-1]["event"] == "endpoint":
                    message_endpoint = data_str.strip()
                    log_success(f"获取到 message endpoint: {message_endpoint}")
                    break

            iter_count += 1
            if time.time() - start_time > 15:
                log_error("等待 endpoint 事件超时 (15s)")
                break
    except Exception as e:
        log_error(f"SSE 读取异常: {e}")
        return 1

    if not message_endpoint:
        log_error("未能获取 message endpoint")
        log_info("收到的 SSE 事件:")
        for ev in sse_events[:10]:
            log_info(f"  event={ev['event']}, data={ev['data'][:200]}")
        return 1

    # 构造完整的 message endpoint URL
    message_url = message_endpoint
    if message_url.startswith("/"):
        # 如果是相对路径，基于 SSE URL 的基础 URL 构造
        from urllib.parse import urlparse
        parsed = urlparse(SSE_URL)
        message_url = f"{parsed.scheme}://{parsed.netloc}{message_url}"
    log_info(f"消息端点完整 URL: {message_url}")

    # ─── Step 3: 发送 initialize ──────────────────────────────
    log_step("3/5", "发送 MCP initialize 请求")

    init_req = make_jsonrpc_request(
        "initialize",
        {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {
                "name": "mcp-gateway-tester",
                "version": "1.0.0",
            },
        },
    )

    log_info(f"请求 ID: {init_req['id']}")
    log_info(f"请求方法: {init_req['method']}")

    # 发送请求到 message endpoint
    post_headers = {
        "Authorization": f"Bearer {AUTH_TOKEN}",
        "Content-Type": "application/json",
    }

    try:
        init_resp = requests.post(
            message_url, headers=post_headers,
            json=init_req, timeout=10,
        )
        log_success(f"HTTP 状态: {init_resp.status_code}")
        init_result = init_resp.json()
        log_info(f"响应: {json.dumps(init_result, indent=2, ensure_ascii=False)[:1000]}")
    except Exception as e:
        log_error(f"initialize 请求失败: {e}")
        failed = True
        init_result = None

    # 等待并检查 SSE 流中的 initialize 结果
    # (initialize 的响应可能通过 SSE 返回)
    time.sleep(1)

    # ─── Step 4: 发送 tools/list ──────────────────────────────
    log_step("4/5", "发送 tools/list 请求")

    tools_req = make_jsonrpc_request("tools/list")

    try:
        tools_resp = requests.post(
            message_url, headers=post_headers,
            json=tools_req, timeout=10,
        )
        log_success(f"HTTP 状态: {tools_resp.status_code}")
        tools_result = tools_resp.json()
        log_info(f"完整响应: {json.dumps(tools_result, indent=2, ensure_ascii=False)[:3000]}")
    except Exception as e:
        log_error(f"tools/list 请求失败: {e}")
        failed = True
        tools_result = None

    # ─── Step 5: 尝试调用一个工具 ──────────────────────────────
    log_step("5/5", "尝试调用工具 (list_knowledge_bases)")

    call_req = make_jsonrpc_request(
        "tools/call",
        {"name": "list_knowledge_bases", "arguments": {}},
    )

    try:
        call_resp = requests.post(
            message_url, headers=post_headers,
            json=call_req, timeout=30,
        )
        log_success(f"HTTP 状态: {call_resp.status_code}")
        call_result = call_resp.json()
        log_info(f"完整响应: {json.dumps(call_result, indent=2, ensure_ascii=False)[:3000]}")
    except Exception as e:
        log_error(f"tools/call 请求失败: {e}")
        failed = True
        call_result = None

    # ─── 清理 ──────────────────────────────────────────────────
    log_step("清理", "关闭 SSE 连接")
    resp.close()

    # ─── 汇总 ──────────────────────────────────────────────────
    print(f"\n{'='*60}")
    if failed:
        print("  测试结果: ❌ 部分测试失败")
        return 1
    else:
        print("  测试结果: ✅ 所有测试通过")
        return 0


if __name__ == "__main__":
    sys.exit(main())
