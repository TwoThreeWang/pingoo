# Pingoo API 使用文档

发布于 2025年9月14日 · 最后更新 2025年9月25日

## 基本信息

- **Base URL**: `http://localhost:5004/api`
- **认证方式**: Bearer Token (JWT)
- **响应格式**: JSON
- **请求头**:
```json
{
  "Authorization": "Bearer yourtoken"
}
```

## API 路径

### 认证相关

#### 用户注册
- **URL**: `/auth/register`
- **方法**: POST
- **认证**: 不需要
- **请求体**:
```json
{
  "email": "user@example.com",
  "password": "password123"
}
```
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoyLCJ1c2VybmFtZSI6InVzZXIiLCJlbWFpbCI6InVzZXJAZXhhbXBsZS5jb20iLCJyb2xlIjoidXNlciIsImV4cCI6MTc1ODk1NDc3OX0.VjO5VWdu3qrRUceRh9Mn0wSRiankKoFmmSypSx4EFX4",
        "refresh_token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VyX2lkIjoyLCJ1c2VybmFtZSI6InVzZXIiLCJlbWFpbCI6InVzZXJAZXhhbXBsZS5jb20iLCJyb2xlIjoidXNlciIsImV4cCI6MTc1OTMwMDM3OX0.Ucy4yGOqhPYSzSU3MbrVT3X943WDAi_I1-IRbrr0Q-U",
        "user": {
            "id": 1,
            "username": "user",
            "email": "user@example.com",
            "role": "user"
        },
        "expires_in": 259200
    }
}
```

#### 用户登录
- **URL**: `/auth/login`
- **方法**: POST
- **认证**: 不需要
- **请求体**:
```json
{
  "email": "user@example.com",
  "password": "password123"
}
```
- **响应**: 同注册接口

#### 刷新Token
- **URL**: `/auth/refresh`
- **方法**: POST
- **认证**: 不需要
- **请求体**:
```json
{
  "refresh_token": "refresh_token"
}
```
- **响应**: 同登录接口

#### 获取当前用户信息
- **URL**: `/auth/me`
- **方法**: GET
- **认证**: 需要
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 2,
        "username": "user",
        "email": "user@example.com",
        "role": "user"
    }
}
```

#### 更新用户资料
- **URL**: `/auth/profile`
- **方法**: PUT
- **认证**: 需要
- **请求体**:
```json
{
  "username": "new_username",
  "email": "new_email@example.com"
}
```
- **响应**: 同获取用户信息接口

#### 更新用户密码
- **URL**: `/auth/password`
- **方法**: PUT
- **认证**: 需要
- **请求体**:
```json
{
  "old_password": "password123",
  "new_password": "password1234"
}
```
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": "密码修改成功"
}
```

### 事件相关

#### 创建事件
- **URL**: `/events`
- **方法**: POST
- **认证**: 需要
- **请求体**:
```json
{
  "site_id": 1,
  "session_id": "session_123",
  "user_id": "user_456",  // 非必填，用于跟追踪网站用户系统关联
  "ip": "66.249.72.20", // 非必填，如果为空后台会获取请求者IP
  "url": "https://example.com/page",
  "referrer": "https://google.com",
  "user_agent": "Mozilla/5.0...",
  "screen": "1920x1080",
  "event_type": "page_view",
  "event_value": "homepage"
}
```
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "ID": 153,
        "CreatedAt": "2025-09-24T14:58:37.7937739+08:00",
        "UpdatedAt": "2025-09-24T14:58:37.7937739+08:00",
        "DeletedAt": null,
        "site_id": 1,
        "session_id": "session_123",
        "user_id": "user_456",
        "ip": "66.249.72.20",
        "url": "https://example.com/page",
        "referrer": "https://google.com",
        "user_agent": "Mozilla/5.0...",
        "device": "desktop",
        "browser": "Mozilla 5.0...",
        "os": "",
        "screen": "1920x1080",
        "is_bot": false,
        "country": "美国",
        "subdivision": "",
        "city": "",
        "isp": "Google",
        "event_type": "page_view",
        "event_value": "homepage",
        "site": {
            "ID": 0,
            "CreatedAt": "0001-01-01T00:00:00Z",
            "UpdatedAt": "0001-01-01T00:00:00Z",
            "DeletedAt": null,
            "user_id": 0,
            "name": "",
            "domain": "",
            "user": {
                "ID": 0,
                "CreatedAt": "0001-01-01T00:00:00Z",
                "UpdatedAt": "0001-01-01T00:00:00Z",
                "DeletedAt": null,
                "username": "",
                "email": "",
                "role": ""
            }
        }
    }
}
```

#### 获取事件列表
- **URL**: `/events/:site_id`
- **方法**: GET
- **认证**: 需要
- **查询参数**:
  - `page` (默认1)
  - `page_size` (默认10)
  - `session_id`
  - `user_id`
  - `event_type`
  - `start_time`
  - `end_time`
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "list": [{
                "ID": 153,
                "CreatedAt": "2025-09-24T14:58:37.793773+08:00",
                "UpdatedAt": "2025-09-24T14:58:37.793773+08:00",
                "DeletedAt": null,
                "site_id": 1,
                "session_id": "session_123",
                "user_id": "user_456",
                "ip": "66.249.72.20",
                "url": "https://example.com/page",
                "referrer": "https://google.com",
                "user_agent": "Mozilla/5.0...",
                "device": "desktop",
                "browser": "Mozilla 5.0...",
                "os": "",
                "screen": "1920x1080",
                "is_bot": false,
                "country": "美国",
                "subdivision": "",
                "city": "",
                "isp": "Google",
                "event_type": "page_view",
                "event_value": "homepage",
                "site": {
                    "ID": 0,
                    "CreatedAt": "0001-01-01T00:00:00Z",
                    "UpdatedAt": "0001-01-01T00:00:00Z",
                    "DeletedAt": null,
                    "user_id": 0,
                    "name": "",
                    "domain": "",
                    "user": {
                        "ID": 0,
                        "CreatedAt": "0001-01-01T00:00:00Z",
                        "UpdatedAt": "0001-01-01T00:00:00Z",
                        "DeletedAt": null,
                        "username": "",
                        "email": "",
                        "role": ""
                    }
                }
            }, {
                "ID": 152,
                "CreatedAt": "2025-09-24T14:54:45.980443+08:00",
                "UpdatedAt": "2025-09-24T14:54:45.980443+08:00",
                "DeletedAt": null,
                "site_id": 1,
                "session_id": "session_123",
                "user_id": "user_456",
                "ip": "::1",
                "url": "https://example.com/page",
                "referrer": "https://google.com",
                "user_agent": "Mozilla/5.0...",
                "device": "desktop",
                "browser": "Mozilla 5.0...",
                "os": "",
                "screen": "1920x1080",
                "is_bot": false,
                "country": "纯真网络",
                "subdivision": "",
                "city": "",
                "isp": "2025年07月09日IP数据",
                "event_type": "page_view",
                "event_value": "homepage",
                "site": {
                    "ID": 0,
                    "CreatedAt": "0001-01-01T00:00:00Z",
                    "UpdatedAt": "0001-01-01T00:00:00Z",
                    "DeletedAt": null,
                    "user_id": 0,
                    "name": "",
                    "domain": "",
                    "user": {
                        "ID": 0,
                        "CreatedAt": "0001-01-01T00:00:00Z",
                        "UpdatedAt": "0001-01-01T00:00:00Z",
                        "DeletedAt": null,
                        "username": "",
                        "email": "",
                        "role": ""
                    }
                }
            }
        ],
        "total": 153,
        "page": 1,
        "page_size": 10
    }
}
```

#### 获取事件统计排名
- **URL**: `/events/:site_id/stats`
- **方法**: GET
- **认证**: 需要
- **查询参数**:
  - `date` (默认当天，格式：20250915)
  - `page` (默认1)
  - `stat_type` (默认"url")
  - `event_type` (默认"page_view")
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "list": [
            {
                "key": "/websites/1",
                "count": 31
            },
            {
                "key": "/",
                "count": 16
            }
        ],
        "total": 2,
        "page": 1,
        "page_size": 10
    }
}
```

#### 获取网站下整体流量指标
- **URL**: `/events/:site_id/summary`
- **方法**: GET
- **认证**: 需要
- **查询参数**: `date` (默认当天)
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "site_id": 1,
        "pv": 81,
        "uv": 3,
        "ip_count": 3,
        "event_count": 0,
        "bounce_rate": 0,
        "avg_duration": 2016,
        "week_ip": 3,
        "week_pv": 134,
        "month_ip": 4,
        "month_pv": 142,
        "hourly_stats": [{
            "hour": 15,
            "count": 27
        }],
        "start_time": "20250915",
        "end_time": "20250915"
    }
}
```

### 站点相关

#### 创建站点
- **URL**: `/sites`
- **方法**: POST
- **认证**: 需要
- **请求体**:
```json
{
  "name": "我的网站",
  "domain": "https://example.com"
}
```
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 2,
        "user_id": 1,
        "name": "我的网站",
        "domain": "https://example.com",
        "tracking_id": "",
        "created_at": "2025-09-24T17:38:33.7565505+08:00",
        "updated_at": "2025-09-24T17:38:33.7565505+08:00"
    }
}
```

#### 获取站点列表
- **URL**: `/sites`
- **方法**: GET
- **认证**: 需要
- **查询参数**:
  - `page` (默认1)
  - `limit` (默认10)
  - `search` (可选)
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "list": [
            {
                "id": 2,
                "user_id": 1,
                "name": "我的网站",
                "domain": "https://example.com",
                "tracking_id": "",
                "created_at": "2025-09-24T17:38:33.75655+08:00",
                "updated_at": "2025-09-24T17:38:33.75655+08:00"
            },
            {
                "id": 1,
                "user_id": 1,
                "name": "Pingoo",
                "domain": "http://127.0.0.1:5004/",
                "tracking_id": "",
                "created_at": "2025-09-15T15:35:28.141283+08:00",
                "updated_at": "2025-09-15T15:35:28.141283+08:00"
            }
        ],
        "total": 2,
        "page": 1,
        "page_size": 10
    }
}
```

#### 获取站点详情
- **URL**: `/sites/:id`
- **方法**: GET
- **认证**: 需要
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 2,
        "user_id": 1,
        "name": "我的网站",
        "domain": "https://example.com",
        "tracking_id": "",
        "created_at": "2025-09-24T17:38:33.75655+08:00",
        "updated_at": "2025-09-24T17:38:33.75655+08:00"
    }
}
```

#### 更新站点信息
- **URL**: `/sites/:id`
- **方法**: PUT
- **认证**: 需要
- **请求体**:
```json
{
  "name": "新站点名称",
  "domain": "https://newdomain.com"
}
```
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "id": 2,
        "user_id": 1,
        "name": "新站点名称",
        "domain": "https://newdomain.com",
        "tracking_id": "",
        "created_at": "2025-09-24T17:38:33.75655+08:00",
        "updated_at": "2025-09-25T14:21:00.8914916+08:00"
    }
}
```

#### 删除站点
- **URL**: `/sites/:id`
- **方法**: DELETE
- **认证**: 需要
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": "站点删除成功"
}
```

#### 清空站点统计
- **URL**: `/sites/:id/stats`
- **方法**: DELETE
- **认证**: 需要
- **响应**:
```json
{
    "code": 0,
    "msg": "success",
    "data": {
        "message": "统计数据已清空"
    }
}
```

#### 健康检查
- **URL**: `/health`
- **方法**: GET
- **认证**: 不需要
- **响应**:
```json
{
    "message": "服务运行正常",
    "status": "ok"
}
```

### 错误

#### 认证错误，缺失认证请求头
```json
{
    "error": "Authorization header required"
}
```

#### 认证错误，Token 失效
```json
{
    "error": "Invalid token: token is malformed: could not base64 decode header: illegal base64 data at input byte 36"
}
```