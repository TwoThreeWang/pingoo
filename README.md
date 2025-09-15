# Pingoo - 轻量的网站统计分析系统

基于 Gin 框架的轻量级网站统计分析系统，提供事件追踪、页面浏览统计和用户行为分析功能。专为中小型网站设计，易于部署和集成。

## 功能特性

### 数据追踪
- ✅ 页面浏览（PV）和访客（UV）统计
- ✅ 自定义事件追踪（点击、表单提交等）
- ✅ 会话（Session）管理和用户行为分析
- ✅ 访问来源和引荐网站统计
- ✅ 设备、浏览器和操作系统分析

### 实时分析
- ✅ 实时访问数据统计和展示
- ✅ 自定义时间范围数据分析
- ✅ 数据可视化图表和报表
- ✅ 热门页面和访问路径分析
- ✅ 用户行为漏斗分析

### 系统功能
- ✅ RESTful API 设计，支持第三方集成
- ✅ JWT 认证和权限控制
- ✅ 多站点管理和数据隔离
- ✅ 响应式 Web 界面

### 易用性
- ✅ 一键部署，支持 Docker 容器化
- ✅ 简单的 JavaScript 埋点代码
- ✅ 自动生成站点统计代码
- ✅ 完善的 API 文档和使用指南
- ✅ 支持自主删除数据

## 技术栈

- **后端框架**: Gin (Go)
- **数据库**: GORM（支持SQLite、MySQL、PostgreSQL）
- **配置管理**: 环境变量 + godotenv
- **API文档**: RESTful设计

## 快速开始

### 1. 克隆项目

```bash
git clone https://github.com/TwoThreeWang/pingoo.git
cd pingoo
```

### 2. 环境要求

- Go 1.21+
- PostgreSQL

### 3. 安装依赖

```bash
go mod download
```

### 4. 配置环境变量

复制 `.env.example` 为 `.env` 并根据需要修改配置：

```bash
cp .env.example .env
```

编辑 `.env` 文件，设置配置：
```bash
# 服务器配置
SERVER_PORT=5004    # 运行端口
GIN_MODE=debug      # 运行模式

# 网站配置
SITE_NAME=Pingoo                  # 网站名称
SITE_DOMAIN=http://localhost:5004 # 网站域名
VERSION=1.0.0                     # 程序版本
TRACKER_SCRIPT_NAME=pingoo.js     # 追踪JS文件自定义名称（防止被广告拦截）

# PostgreSQL配置
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=pingoo
DB_SSLMODE=disable

# JWT配置
JWT_SECRET_KEY=your-secret-key-here
JWT_EXPIRE_HOURS=72
JWT_REFRESH_EXPIRE=168
```

### 5. 运行项目

```bash
go run main.go
```

服务将在 `http://localhost:5004` 启动

### 6. 使用教程

1. 通过页面或者 API 接口创建用户
2. 添加要统计的网站，获取 SiteID
3. 在对应网站上添加统计代码
4. 访问你的网站，然后在Pingoo仪表盘中查看实时数据

将追踪代码添加到你的网站 `<head>` 标签内：

```html
<!DOCTYPE html>
<html>
<head>
    <script src='http://localhost:8080/pingoo.js' site-id='1'></script>
    <title>我的网站</title>
</head>
<body>
    <!-- 你的网站内容 -->
</body>
</html>
```

## API文档

详细API文档请参考 [API.md](docs/api.md) 文件，包含以下主要内容：

- **用户认证API**：注册、登录、获取当前用户信息
- **站点管理API**：创建站点、获取站点列表、站点详情、站点统计
- **事件追踪API**：页面浏览追踪、自定义事件追踪
- **前端埋点代码**：自动生成的追踪代码集成指南
- **错误处理**：各种错误情况的响应格式

## 项目结构

```
pingoo/
├── main.go                 # 主程序入口
├── config/                 # 配置文件
│   └── config.go          # 配置结构体
├── controllers/           # 控制器层
│   ├── auth_controller.go  # 认证控制器（注册、登录、用户信息）
│   ├── event_controller.go # 事件控制器
│   ├── render.go          # 渲染控制器
│   ├── site_controller.go # 站点管理控制器
│   └── web_controller.go  # Web页面控制器
├── database/              # 数据库相关
│   ├── database.go        # 数据库连接
│   └── migrations.go      # 数据库迁移
├── middleware/            # 中间件
│   ├── auth.go            # JWT认证中间件
│   └── cors.go            # CORS中间件
├── models/                # 数据模型
│   ├── event.go           # 事件模型
│   ├── session.go         # 会话模型
│   ├── site.go            # 站点模型
│   ├── stats.go           # 统计模型
│   └── user.go            # 用户模型
├── routers/               # 路由配置
│   └── router.go          # 路由设置
├── services/              # 业务逻辑层
│   ├── event_service.go   # 事件服务
│   └── site_service.go    # 站点服务
├── utils/                 # 工具函数
│   ├── response.go        # 响应工具
│   └── ua_parser.go       # UserAgent解析
├── templates/             # HTML模板
│   ├── base.html          # 基础模板
│   ├── index.html         # 首页
│   ├── dashboard.html     # 仪表盘
│   ├── login.html         # 登录页面
│   ├── register.html      # 注册页面
│   ├── profile.html       # 个人资料页面
│   ├── websites.html      # 站点管理页面
│   └── 404.html           # 404页面
├── public/                # 静态资源
│   ├── css/               # 样式文件
│   ├── js/                # JavaScript文件
│   ├── pingoo.js          # 前端埋点SDK
│   └── img/               # 图片资源
├── docs/                  # 文档目录
├── .env                   # 配置文件
├── .env.example           # 环境变量示例
├── go.mod                 # Go模块文件
├── go.sum                 # Go依赖锁定文件
├── README.md              # 项目说明
├── LICENSE                # 许可证文件
├── Dockerfile             # Docker配置
├── docker-compose.yml     # Docker Compose配置
└── deploy.sh              # 部署脚本
```

## 开发指南

### 添加新功能

1. **添加新API端点**:
   - 在 `routers/router.go` 中添加路由
   - 在 `controllers/` 中创建对应的控制器
   - 在 `services/` 中创建对应的业务逻辑

2. **添加新数据模型**:
   - 在 `models/` 中创建新的模型结构
   - 使用GORM标签定义数据库字段
   - 在 `main.go` 中自动迁移数据库

## 贡献指南

1. Fork项目
2. 创建功能分支 (`git checkout -b feature/AmazingFeature`)
3. 提交更改 (`git commit -m 'Add some AmazingFeature'`)
4. 推送到分支 (`git push origin feature/AmazingFeature`)
5. 创建Pull Request

## 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件

## 联系方式

- 项目地址: [https://github.com/TwoThreeWang/pingoo](https://github.com/TwoThreeWang/pingoo)
- 问题反馈: [Issues](https://github.com/TwoThreeWang/pingoo/issues)