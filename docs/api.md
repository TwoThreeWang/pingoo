# Pingoo API 使用文档

🚀 通过 Pingoo API 获取网站统计数据，实现自定义报表、分析和二次开发

## 🚀 快速开始

### 基本信息

| 项目 | 值 |
|------|-----|
| **Base URL** | `http://localhost:5004/api` |
| **认证方式** | Bearer Token (JWT) |
| **响应格式** | JSON |
| **最后更新** | 2025年9月27日 |

### 请求头配置

```json
{
  "Authorization": "Bearer yourtoken",
  "Content-Type": "application/json"
}
```

---

## 🔐 认证相关

### 用户注册

创建新用户账户

**请求信息**
- **URL**: `/auth/register`
- **方法**: `POST`
- **认证**: ❌ 不需要

**请求参数**

```json
{
  "email": "user@example.com",
  "password": "password123"
}
```

**响应示例**

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
    "expires_in": 259200
  }
}
```

### 用户登录

用户身份认证

**请求信息**
- **URL**: `/auth/login`
- **方法**: `POST`
- **认证**: ❌ 不需要

**请求参数**

```json
{
  "email": "user@example.com",
  "password": "password123"
}
```

**响应示例**

响应格式与注册接口相同。

### 刷新Token

更新访问令牌

**请求信息**
- **URL**: `/auth/refresh`
- **方法**: `POST`
- **认证**: ❌ 不需要

**请求参数**

```json
{
  "refresh_token": "refresh_token"
}
```

### 获取当前用户信息

获取已登录用户的详细信息

**请求信息**
- **URL**: `/auth/me`
- **方法**: `GET`
- **认证**: ✅ 需要

**响应示例**

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

### 更新用户资料

修改用户基本信息

**请求信息**
- **URL**: `/auth/profile`
- **方法**: `PUT`
- **认证**: ✅ 需要

**请求参数**

```json
{
  "username": "new_username",
  "email": "new_email@example.com"
}
```

### 更新用户密码

修改用户登录密码

**请求信息**
- **URL**: `/auth/password`
- **方法**: `PUT`
- **认证**: ✅ 需要

**请求参数**

```json
{
  "old_password": "password123",
  "new_password": "password1234"
}
```

**响应示例**

```json
{
  "code": 0,
  "msg": "success",
  "data": "密码修改成功"
}
```

---

## 📊 事件相关

### 创建事件

记录用户行为事件

**请求信息**
- **URL**: `/events`
- **方法**: `POST`
- **认证**: ✅ 需要

**请求参数**

| 字段 | 类型 | 必填 | 描述 |
|------|------|------|------|
| `site_id` | `int` | ✅ | 站点ID |
| `session_id` | `string` | ✅ | 会话ID |
| `user_id` | `string` | ❌ | 用户ID（用于关联网站用户系统） |
| `ip` | `string` | ❌ | IP地址（为空时自动获取） |
| `url` | `string` | ✅ | 页面URL |
| `referrer` | `string` | ❌ | 来源页面 |
| `user_agent` | `string` | ✅ | 用户代理 |
| `screen` | `string` | ❌ | 屏幕分辨率 |
| `event_type` | `string` | ✅ | 事件类型 |
| `event_value` | `string` | ❌ | 事件值 |

**请求示例**

```json
{
  "site_id": 1,
  "session_id": "session_123",
  "user_id": "user_456",
  "ip": "66.249.72.20",
  "url": "https://example.com/page",
  "referrer": "https://google.com",
  "user_agent": "Mozilla/5.0...",
  "screen": "1920x1080",
  "event_type": "page_view",
  "event_value": "homepage"
}
```

**响应示例**

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "ID": 153,
    "CreatedAt": "2025-09-24T14:58:37.7937739+08:00",
    "UpdatedAt": "2025-09-24T14:58:37.7937739+08:00",
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
    "event_value": "homepage"
  }
}
```

### 获取事件列表

获取指定站点的事件记录

**请求信息**
- **URL**: `/events/:site_id`
- **方法**: `GET`
- **认证**: ✅ 需要

**查询参数**

| 参数 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `page` | `int` | `1` | 页码 |
| `page_size` | `int` | `10` | 每页数量 |
| `session_id` | `string` | - | 会话ID筛选 |
| `user_id` | `string` | - | 用户ID筛选 |
| `event_type` | `string` | - | 事件类型筛选 |
| `start_time` | `string` | - | 开始时间 |
| `end_time` | `string` | - | 结束时间 |

**使用示例**

```
GET /api/events/1?page=1&page_size=20&event_type=page_view
```
**响应示例**

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

### 获取事件统计排名

获取事件的统计排名数据

**请求信息**
- **URL**: `/events/:site_id/stats`
- **方法**: `GET`
- **认证**: ✅ 需要

**查询参数**

| 参数 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `date` | `string` | 当天 | 日期（格式：20250915） |
| `page` | `int` | `1` | 页码 |
| `stat_type` | `string` | `"url"` | 统计类型 |
| `event_type` | `string` | `"page_view"` | 事件类型 |

**响应示例**

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

### 获取网站整体流量指标

获取站点的综合统计数据

**请求信息**
- **URL**: `/events/:site_id/summary`
- **方法**: `GET`
- **认证**: ✅ 需要

**查询参数**

| 参数 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `date` | `string` | 当天 | 统计日期 |

**响应示例**

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
    "hourly_stats": [
      {
        "hour": 15,
        "count": 27
      }
    ],
    "start_time": "20250915",
    "end_time": "20250915"
  }
}
```

**数据字段说明**

| 字段 | 描述 |
|------|------|
| `pv` | 页面浏览量 |
| `uv` | 独立访客数 |
| `ip_count` | IP数量 |
| `event_count` | 事件总数 |
| `bounce_rate` | 跳出率 |
| `avg_duration` | 平均停留时长（秒） |
| `week_ip` | 周IP数量 |
| `week_pv` | 周页面浏览量 |
| `month_ip` | 月IP数量 |
| `month_pv` | 月页面浏览量 |
| `hourly_stats` | 小时统计数据 |

---

## 🌐 站点相关

### 创建站点

添加新的监控站点

**请求信息**
- **URL**: `/sites`
- **方法**: `POST`
- **认证**: ✅ 需要

**请求参数**

```json
{
  "name": "我的网站",
  "domain": "https://example.com"
}
```

**响应示例**

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "id": 2,
    "user_id": 1,
    "name": "我的网站",
    "domain": "https://example.com",
    "created_at": "2025-09-24T17:38:33.7565505+08:00",
    "updated_at": "2025-09-24T17:38:33.7565505+08:00"
  }
}
```

### 获取站点列表

获取用户的所有站点

**请求信息**
- **URL**: `/sites`
- **方法**: `GET`
- **认证**: ✅ 需要

**查询参数**

| 参数 | 类型 | 默认值 | 描述 |
|------|------|--------|------|
| `page` | `int` | `1` | 页码 |
| `limit` | `int` | `10` | 每页数量 |
| `search` | `string` | - | 搜索关键词 |

**响应示例**

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

### 获取站点详情

获取指定站点的详细信息

**请求信息**
- **URL**: `/sites/:id`
- **方法**: `GET`
- **认证**: ✅ 需要

**响应示例**

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

### 更新站点信息

修改站点基本信息

**请求信息**
- **URL**: `/sites/:id`
- **方法**: `PUT`
- **认证**: ✅ 需要

**请求参数**

```json
{
  "name": "新站点名称",
  "domain": "https://newdomain.com"
}
```
**响应示例**

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

### 删除站点

删除指定站点及其相关数据

**请求信息**
- **URL**: `/sites/:id`
- **方法**: `DELETE`
- **认证**: ✅ 需要

**响应示例**

```json
{
  "code": 0,
  "msg": "success",
  "data": "站点删除成功"
}
```

### 清空站点统计

清空指定站点的所有统计数据

**请求信息**
- **URL**: `/sites/:id/stats`
- **方法**: `DELETE`
- **认证**: ✅ 需要

**响应示例**

```json
{
  "code": 0,
  "msg": "success",
  "data": {
    "message": "统计数据已清空"
  }
}
```

---

## ⚡ 其他接口

### 健康检查

检查API服务状态

**请求信息**
- **URL**: `/health`
- **方法**: `GET`
- **认证**: ❌ 不需要

**响应示例**

```json
{
  "code": 0,
  "msg": "服务运行正常"
}
```

---

## ❌ 错误处理

### 常见错误码

| 错误码 | 说明 | 解决方案 |
|--------|------|----------|
| `401` | 认证失败 | 检查token是否有效 |
| `403` | 权限不足 | 确认用户权限 |
| `404` | 资源不存在 | 检查请求路径和参数 |
| `500` | 服务器错误 | 联系技术支持 |

### 错误响应格式

```json
{
  "code": 1,
  "msg": "错误描述",
  "data": null
}
```

### 认证相关错误

**缺失认证请求头**
```json
{
  "error": "Authorization header required"
}
```

**Token失效**
```json
{
  "error": "Invalid token: token is malformed: could not base64 decode header: illegal base64 data at input byte 36"
}
```

---

## 📖 使用示例

### JavaScript/Node.js

```javascript
// 获取站点列表
const response = await fetch('http://localhost:5004/api/sites', {
  method: 'GET',
  headers: {
    'Authorization': 'Bearer your-token-here',
    'Content-Type': 'application/json'
  }
});

const data = await response.json();
console.log(data);
```

### Python

```python
import requests

# 创建事件
url = "http://localhost:5004/api/events"
headers = {
    "Authorization": "Bearer your-token-here",
    "Content-Type": "application/json"
}

data = {
    "site_id": 1,
    "session_id": "session_123",
    "url": "https://example.com/page",
    "event_type": "page_view",
    "event_value": "homepage"
}

response = requests.post(url, headers=headers, json=data)
print(response.json())
```

### cURL

```bash
# 用户登录
curl -X POST http://localhost:5004/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "user@example.com",
    "password": "password123"
  }'
```

---

最后更新日期：2025年9月27日