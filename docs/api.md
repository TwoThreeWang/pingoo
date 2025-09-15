# Pingoo API 使用文档

发布于 2025年9月14日 · 最后更新 2025年9月15日

** AI生成的，将就看，等项目完善了再补 **

## 基础信息

### 服务器信息

- **Base URL**: `http://localhost:8080`
- **API Base Path**: `/api`
- **Content-Type**: `application/json`

### 响应格式

所有 API 响应都使用统一的 JSON 格式：

```json
{
    "code": 0,          // HTTP 状态码
    "msg": "success", // 响应消息
    "data": {}           // 响应数据，可能是对象、数组或 null
}
```

### 分页格式

对于返回列表数据的接口，统一使用以下分页格式：

```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "list": [],     // 数据项数组
        "total": 100,    // 总记录数
        "page": 1,       // 当前页码
        "page_size": 10      // 每页记录数
    }
}
```

### 错误处理

当发生错误时，服务器会返回相应的 HTTP 状态码和错误信息：

```json
{
    "code": 1,           // HTTP 错误状态码
    "msg": "错误消息",  // 具体的错误描述
    "data": null
}
```

常见错误状态码：

- `400 Bad Request`: 请求参数错误
- `401 Unauthorized`: 未认证或认证失败
- `403 Forbidden`: 权限不足
- `404 Not Found`: 资源不存在
- `422 Unprocessable Entity`: 请求参数验证失败
- `500 Internal Server Error`: 服务器内部错误

## 认证

系统采用JWT认证机制，所有需要认证的API端点都需要在请求头中添加：

```
Authorization: Bearer <your_jwt_token>
```

## 用户认证 API

### 用户注册

- **URL**: `/api/auth/register`
- **Method**: `POST`
- **请求体**:
```json
{
    "email": "user@example.com",
    "password": "securepassword123"
}
```

## 事件追踪 API

### 获取事件列表

- **URL**: `/api/events`
- **Method**: `GET`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **查询参数**:
  - `page`: 页码，默认为1
  - `limit`: 每页数量，默认为10
  - `site_id`: 站点ID
  - `event_type`: 事件类型（可选）
  - `start_date`: 开始日期，格式为 YYYY-MM-DD（可选）
  - `end_date`: 结束日期，格式为 YYYY-MM-DD（可选）
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "items": [
            {
                "id": 1,
                "site_id": 1,
                "session_id": "sess_1631577600_abc123",
                "user_id": "user_123",
                "ip": "127.0.0.1",
                "url": "https://example.com/page",
                "referrer": "https://google.com",
                "user_agent": "Mozilla/5.0...",
                "device": "desktop",
                "browser": "Chrome",
                "os": "Windows",
                "screen": "1920x1080",
                "event_type": "page_view",
                "event_value": "首页",
                "created_at": "2025-09-14T10:00:00Z"
            }
        ],
        "total": 1,
        "page": 1,
        "limit": 10
    }
}
```

### 创建事件

- **URL**: `/api/events`
- **Method**: `POST`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **请求体**:
```json
{
    "site_id": 1,
    "session_id": "sess_1631577600_abc123",
    "user_id": "user_123",
    "url": "https://example.com/page",
    "referrer": "https://google.com",
    "event_type": "page_view",
    "event_value": "首页"
}
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 1,
        "site_id": 1,
        "session_id": "sess_1631577600_abc123",
        "user_id": "user_123",
        "ip": "127.0.0.1",
        "url": "https://example.com/page",
        "referrer": "https://google.com",
        "user_agent": "Mozilla/5.0...",
        "device": "desktop",
        "browser": "Chrome",
        "os": "Windows",
        "screen": "1920x1080",
        "event_type": "page_view",
        "event_value": "首页",
        "created_at": "2025-09-14T10:00:00Z"
    }
}
```

### 获取事件统计

- **URL**: `/api/events/stats`
- **Method**: `GET`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **查询参数**:
  - `site_id`: 站点ID
  - `event_type`: 事件类型（可选）
  - `start_date`: 开始日期，格式为 YYYY-MM-DD（可选）
  - `end_date`: 结束日期，格式为 YYYY-MM-DD（可选）
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "total_events": 1000,
        "unique_visitors": 500,
        "page_views": 800,
        "avg_time_on_site": 300,
        "bounce_rate": 0.25,
        "event_types": [
            {
                "type": "page_view",
                "count": 800
            },
            {
                "type": "button_click",
                "count": 0
            }
        ]
    }
}
```

### 获取单个事件

- **URL**: `/api/events/:id`
- **Method**: `GET`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 1,
        "site_id": 1,
        "session_id": "sess_1631577600_abc123",
        "user_id": "user_123",
        "ip": "127.0.0.1",
        "url": "https://example.com/page",
        "referrer": "https://google.com",
        "user_agent": "Mozilla/5.0...",
        "device": "desktop",
        "browser": "Chrome",
        "os": "Windows",
        "screen": "1920x1080",
        "event_type": "page_view",
        "event_value": "首页",
        "created_at": "2025-09-14T10:00:00Z"
    }
}
```

### 统一事件追踪接口

- **URL**: `/send`
- **Method**: `POST`
- **请求体**:
```json
{
    "site_id": 1,
    "session_id": "sess_1631577600_abc123",
    "user_id": "user_123",
    "url": "https://example.com/page",
    "referrer": "https://google.com",
    "event_type": "page_view",
    "event_value": "首页",
    "custom_data": {
        "button_id": "submit_btn",
        "category": "form"
    }
}
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": null
}
```

## 站点管理 API

### 创建站点

- **URL**: `/api/sites`
- **Method**: `POST`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **请求体**:
```json
{
    "name": "我的网站",
    "domain": "example.com"
}
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 1,
        "name": "我的网站",
        "domain": "example.com",
        "user_id": 1,
        "created_at": "2025-09-14T10:00:00Z",
        "updated_at": "2025-09-14T10:00:00Z"
    }
}
```

### 获取站点列表

- **URL**: `/api/sites`
- **Method**: `GET`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **查询参数**:
  - `page`: 页码，默认为1
  - `limit`: 每页数量，默认为10
  - `search`: 搜索关键词（可选）
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "items": [
            {
                "id": 1,
                "name": "我的网站",
                "domain": "example.com",
                "user_id": 1,
                "created_at": "2025-09-14T10:00:00Z",
                "updated_at": "2025-09-14T10:00:00Z"
            }
        ],
        "total": 1,
        "page": 1,
        "limit": 10
    }
}
```

### 获取站点详情

- **URL**: `/api/sites/:id`
- **Method**: `GET`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 1,
        "name": "我的网站",
        "domain": "example.com",
        "user_id": 1,
        "created_at": "2025-09-14T10:00:00Z",
        "updated_at": "2025-09-14T10:00:00Z"
    }
}
```

### 更新站点信息

- **URL**: `/api/sites/:id`
- **Method**: `PUT`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **请求体**:
```json
{
    "name": "新网站名称",
    "domain": "newdomain.com"
}
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 1,
        "name": "新网站名称",
        "domain": "newdomain.com",
        "user_id": 1,
        "created_at": "2025-09-14T10:00:00Z",
        "updated_at": "2025-09-14T10:00:00Z"
    }
}
```

### 删除站点

- **URL**: `/api/sites/:id`
- **Method**: `DELETE`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": null
}
```

### 获取站点统计信息

- **URL**: `/api/sites/:id/stats`
- **Method**: `GET`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **查询参数**:
  - `date`: 统计日期，格式为 YYYY-MM-DD，默认为当天
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "week_ip": 1000,
        "week_pv": 5000,
        "month_ip": 5000,
        "month_pv": 000,
        "top_referrers": [
            {
                "referrer": "google.com",
                "count": 500
            }
        ],
        "top_os": [
            {
                "os": "Windows",
                "count": 800
            }
        ]
    }
}
```

### 清除站点统计数据

- **URL**: `/api/sites/:id/stats`
- **Method**: `DELETE`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": null
}
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "user": {
            "id": 1,
            "username": "user",
            "email": "user@example.com",
            "role": "user"
        },
        "expires_in": 86400
    }
}
```

### 用户登录

- **URL**: `/api/auth/login`
- **Method**: `POST`
- **请求体**:
```json
{
    "email": "user@example.com",
    "password": "securepassword123"
}
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "user": {
            "id": 1,
            "username": "user",
            "email": "user@example.com",
            "role": "user"
        },
        "expires_in": 86400
    }
}
```

### 刷新令牌

- **URL**: `/api/auth/refresh`
- **Method**: `POST`
- **请求头**:
```
Authorization: Bearer <your_refresh_token>
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
        "expires_in": 86400
    }
}
```

### 获取当前用户信息

- **URL**: `/api/auth/me`
- **Method**: `GET`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 1,
        "username": "user",
        "email": "user@example.com",
        "role": "user"
    }
}
```

### 更新用户资料

- **URL**: `/api/auth/profile`
- **Method**: `PUT`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **请求体**:
```json
{
    "username": "newusername",
    "email": "newemail@example.com"
}
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 1,
        "username": "newusername",
        "email": "newemail@example.com",
        "role": "user"
    }
}
```

### 修改密码

- **URL**: `/api/auth/password`
- **Method**: `PUT`
- **请求头**:
```
Authorization: Bearer <your_jwt_token>
```
- **请求体**:
```json
{
    "old_password": "oldpassword123",
    "new_password": "newpassword123"
}
```
- **响应示例**:
```json
{
    "code": 0,
    "msg": "success",
    "data": null
}
```
