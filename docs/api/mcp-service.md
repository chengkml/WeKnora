# MCP Service API

[返回目录](./README.md)

MCP（Model Context Protocol）服务管理接口，提供 MCP 服务的 CRUD、连通性测试、工具/资源发现。

| 方法   | 路径                                              | 描述                                          |
| ------ | ------------------------------------------------- | --------------------------------------------- |
| POST   | `/mcp-services`                                   | 创建 MCP 服务                                 |
| GET    | `/mcp-services`                                   | 获取当前空间的 MCP 服务列表                   |
| GET    | `/mcp-services/:id`                               | 获取 MCP 服务详情                             |
| PUT    | `/mcp-services/:id`                               | 更新 MCP 服务（部分字段更新）                 |
| DELETE | `/mcp-services/:id`                               | 删除 MCP 服务                                 |
| POST   | `/mcp-services/:id/test`                          | 测试 MCP 服务连通性                           |
| GET    | `/mcp-services/:id/tools`                         | 获取 MCP 服务工具列表                         |
| GET    | `/mcp-services/:id/resources`                     | 获取 MCP 服务资源列表                         |

## POST `/mcp-services` - 创建 MCP 服务

**请求参数**:

| 字段             | 类型    | 必填 | 说明                                                                                          |
| ---------------- | ------- | ---- | --------------------------------------------------------------------------------------------- |
| name             | string  | 是   | 服务名称                                                                                      |
| description      | string  | 否   | 服务描述                                                                                      |
| transport_type   | string  | 是   | 传输类型，可选：`sse`、`http-streamable`、`stdio`                                              |
| url              | string  | 条件 | 服务地址；当 `transport_type` 为 `sse` / `http-streamable` 时必填（受 SSRF 安全校验约束）        |
| headers          | object  | 否   | 自定义请求头                                                                                  |
| auth_config      | object  | 否   | 认证配置，支持 `api_key`、`token`                                                              |
| advanced_config  | object  | 否   | 高级配置，支持 `timeout`、`retry_count`、`retry_delay`                                          |
| stdio_config     | object  | 条件 | stdio 传输配置，包含 `command`、`args`；当 `transport_type` 为 `stdio` 时必填                  |
| env_vars         | object  | 否   | 环境变量（stdio 场景常用）                                                                    |
| enabled          | boolean | 否   | 是否启用                                                                                      |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "天气查询服务",
    "description": "提供全球天气信息查询",
    "transport_type": "sse",
    "url": "https://mcp.example.com/weather/sse",
    "headers": {
        "X-Custom-Header": "value"
    },
    "auth_config": {
        "api_key": "weather-api-key-xxxxx"
    },
    "advanced_config": {
        "timeout": 30,
        "retry_count": 3,
        "retry_delay": 1
    }
}'
```

**响应**:

```json
{
    "data": {
        "id": "mcp-00000001",
        "tenant_id": 1,
        "name": "天气查询服务",
        "description": "提供全球天气信息查询",
        "enabled": true,
        "transport_type": "sse",
        "url": "https://mcp.example.com/weather/sse",
        "headers": {
            "X-Custom-Header": "value"
        },
        "auth_config": {
            "api_key": "weather-api-key-xxxxx"
        },
        "advanced_config": {
            "timeout": 30,
            "retry_count": 3,
            "retry_delay": 1
        },
        "is_builtin": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

**创建 stdio 类型的 MCP 服务示例**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "本地文件服务",
    "description": "通过 stdio 访问本地文件系统",
    "transport_type": "stdio",
    "stdio_config": {
        "command": "/usr/local/bin/mcp-file-server",
        "args": ["--root", "/data"]
    },
    "env_vars": {
        "MCP_LOG_LEVEL": "info"
    }
}'
```

## GET `/mcp-services` - 获取 MCP 服务列表

返回当前空间已配置的所有 MCP 服务。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "mcp-00000001",
            "tenant_id": 1,
            "name": "天气查询服务",
            "description": "提供全球天气信息查询",
            "enabled": true,
            "transport_type": "sse",
            "url": "https://mcp.example.com/weather/sse",
            "headers": {},
            "auth_config": {
                "api_key": "weather-api-key-xxxxx"
            },
            "advanced_config": {
                "timeout": 30,
                "retry_count": 3,
                "retry_delay": 1
            },
            "is_builtin": false,
            "created_at": "2025-08-12T10:00:00+08:00",
            "updated_at": "2025-08-12T10:00:00+08:00"
        },
        {
            "id": "mcp-00000002",
            "tenant_id": 1,
            "name": "本地文件服务",
            "description": "通过 stdio 访问本地文件系统",
            "enabled": true,
            "transport_type": "stdio",
            "headers": {},
            "auth_config": null,
            "advanced_config": null,
            "stdio_config": {
                "command": "/usr/local/bin/mcp-file-server",
                "args": ["--root", "/data"]
            },
            "env_vars": {
                "MCP_LOG_LEVEL": "info"
            },
            "is_builtin": false,
            "created_at": "2025-08-12T11:00:00+08:00",
            "updated_at": "2025-08-12T11:00:00+08:00"
        }
    ],
    "success": true
}
```

## GET `/mcp-services/:id` - 获取 MCP 服务详情

**路径参数**:

| 字段 | 类型   | 说明           |
| ---- | ------ | -------------- |
| id   | string | MCP 服务 ID    |

> 注：内置（`is_builtin: true`）服务在响应中会隐藏敏感凭证字段。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services/mcp-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "id": "mcp-00000001",
        "tenant_id": 1,
        "name": "天气查询服务",
        "description": "提供全球天气信息查询",
        "enabled": true,
        "transport_type": "sse",
        "url": "https://mcp.example.com/weather/sse",
        "headers": {},
        "auth_config": {
            "api_key": "weather-api-key-xxxxx"
        },
        "advanced_config": {
            "timeout": 30,
            "retry_count": 3,
            "retry_delay": 1
        },
        "is_builtin": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

## PUT `/mcp-services/:id` - 更新 MCP 服务

支持部分字段更新，可传入下列任意子集：`name`、`description`、`enabled`、`transport_type`、`url`、`stdio_config`、`env_vars`、`headers`、`auth_config`、`advanced_config`。其中 `url` 若提供，会再次执行 SSRF 安全校验。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/mcp-services/mcp-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "天气查询服务（更新）",
    "description": "提供全球天气信息查询，支持实时数据",
    "enabled": false
}'
```

**响应**:

```json
{
    "data": {
        "id": "mcp-00000001",
        "tenant_id": 1,
        "name": "天气查询服务（更新）",
        "description": "提供全球天气信息查询，支持实时数据",
        "enabled": false,
        "transport_type": "sse",
        "url": "https://mcp.example.com/weather/sse",
        "headers": {},
        "auth_config": {
            "api_key": "weather-api-key-xxxxx"
        },
        "advanced_config": {
            "timeout": 30,
            "retry_count": 3,
            "retry_delay": 1
        },
        "is_builtin": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T12:00:00+08:00"
    },
    "success": true
}
```

## DELETE `/mcp-services/:id` - 删除 MCP 服务

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/mcp-services/mcp-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "success": true,
    "message": "MCP service deleted successfully"
}
```

## POST `/mcp-services/:id/test` - 测试 MCP 服务连通性

后端会以已保存配置建立一次 MCP 连接，返回连接结果及探测到的工具/资源列表。连接失败时 HTTP 仍为 200，但 `data.success` 为 `false`，错误原因在 `data.message` 中。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/mcp-services/mcp-00000001/test' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "success": true,
        "message": "连接成功",
        "description": "提供全球天气信息查询",
        "tools": [
            {
                "name": "get_weather",
                "description": "获取指定城市的天气信息",
                "inputSchema": {
                    "type": "object",
                    "properties": {
                        "city": {
                            "type": "string",
                            "description": "城市名称"
                        }
                    },
                    "required": ["city"]
                }
            }
        ],
        "resources": [
            {
                "uri": "weather://cities",
                "name": "城市列表",
                "description": "支持查询的城市列表",
                "mimeType": "application/json"
            }
        ]
    },
    "success": true
}
```

## GET `/mcp-services/:id/tools` - 获取 MCP 服务工具列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services/mcp-00000001/tools' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "name": "get_weather",
            "description": "获取指定城市的天气信息",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "city": {
                        "type": "string",
                        "description": "城市名称"
                    }
                },
                "required": ["city"]
            }
        },
        {
            "name": "get_forecast",
            "description": "获取未来天气预报",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "city": {
                        "type": "string",
                        "description": "城市名称"
                    },
                    "days": {
                        "type": "integer",
                        "description": "预报天数"
                    }
                },
                "required": ["city"]
            }
        }
    ],
    "success": true
}
```

## GET `/mcp-services/:id/resources` - 获取 MCP 服务资源列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services/mcp-00000001/resources' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "uri": "weather://cities",
            "name": "城市列表",
            "description": "支持查询的城市列表",
            "mimeType": "application/json"
        },
        {
            "uri": "weather://config",
            "name": "服务配置",
            "description": "当前服务配置信息",
            "mimeType": "application/json"
        }
    ],
    "success": true
}
```

