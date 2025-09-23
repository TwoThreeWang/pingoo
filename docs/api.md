# Pingoo API 使用文档

发布于 2025年9月14日 · 最后更新 2025年9月23日

## 基本信息

- **Base URL**: `http://localhost:8080/api`
- **认证方式**: Bearer Token (JWT)
- **响应格式**: JSON

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
  "token": "jwt_token",
  "refresh_token": "refresh_token",
  "user": {
    "id": 1,
    "username": "user",
    "email": "user@example.com",
    "role": "user"
  },
  "expires_in": 86400
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
  "id": 1,
  "username": "user",
  "email": "user@example.com",
  "role": "user"
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
  "user_id": "user_456",
  "url": "https://example.com/page",
  "referrer": "https://google.com",
  "user_agent": "Mozilla/5.0...",
  "device": "Desktop",
  "browser": "Chrome",
  "os": "Windows",
  "screen": "1920x1080",
  "is_bot": false,
  "country": "China",
  "subdivision": "Beijing",
  "city": "Beijing",
  "isp": "China Telecom",
  "event_type": "page_view",
  "event_value": "homepage"
}
```
- **响应**: 创建的事件对象

#### 获取事件列表
- **URL**: `/events/site/:site_id`
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
- **响应**: 分页的事件列表

#### 获取事件统计排名
- **URL**: `/events/site/:site_id/rank`
- **方法**: GET
- **认证**: 需要
- **查询参数**:
  - `date` (默认当天)
  - `page` (默认1)
  - `stat_type` (默认"url")
  - `event_type` (默认"page_view")
- **响应**: 统计排名数据

#### 获取事件摘要
- **URL**: `/events/site/:site_id/summary`
- **方法**: GET
- **认证**: 需要
- **查询参数**: `date` (默认当天)
- **响应**:
```json
{
  "total_pv": 1000,
  "total_uv": 500,
  "bounce_rate": 0.3,
  "avg_duration": 120.5
}
```

#### 自定义事件追踪
- **URL**: `/events/track`
- **方法**: POST
- **认证**: 不需要
- **请求体**:
```json
{
  "session_id": "session_123",
  "user_id": "user_456",
  "url": "https://example.com/page",
  "referrer": "https://google.com",
  "event_type": "custom_event",
  "event_value": "button_click",
  "site_id": "1",
  "screen": "1920x1080"
}
```
- **响应**: 创建的事件对象

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
  "id": 1,
  "user_id": 1,
  "name": "我的网站",
  "domain": "https://example.com",
  "created_at": "2023-01-01T00:00:00Z",
  "updated_at": "2023-01-01T00:00:00Z"
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
- **响应**: 分页的站点列表

#### 获取站点详情
- **URL**: `/sites/:id`
- **方法**: GET
- **认证**: 需要
- **响应**: 站点详情对象

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
- **响应**: 更新后的站点对象

#### 删除站点
- **URL**: `/sites/:id`
- **方法**: DELETE
- **认证**: 需要
- **响应**: "站点删除成功"

#### 清空站点统计
- **URL**: `/sites/:id/stats`
- **方法**: DELETE
- **认证**: 需要
- **响应**: 操作结果