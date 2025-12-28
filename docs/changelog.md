# Pingoo 更新记录

所有重要的更新记录都在此文档中列出。

![骄傲版本控制](https://cdn.c2v2.com/imgur/KfG7NBr.png)

## [2025-12-28] - v1.0.8
- Added
  * 新增定时任务调度器，每天凌晨自动清理60天以前的数据（硬删除）
  * 仪表盘页面增加骨架屏加载效果

- Changed
  * 优化仪表盘加载性能：使用 IntersectionObserver 实现懒加载，仅加载可视区域内的站点统计数据
  * README 优化：添加项目 Logo 和状态徽章，更新 Go 版本要求至 1.23+

- Fixed
  * 无

- Deprecated
  * 无

- Removed
  * 无

## [2025-11-18] - v1.0.7
- Added
  * 无

- Changed
  * 无

- Fixed
  * 将SPA页面跳转的referrer标记为'SPA'
  * 为统计项标签添加title属性以显示完整内容

- Deprecated
  * 无

- Removed
  * 无

## [2025-11-13] - v1.0.6
- Added
  * 添加在线用户统计功能

- Changed
  * 添加对单页应用的路由变化跟踪
  * 改进referrer处理和错误处理
  * 移除未使用的user_agent依赖
  * 优化事件统计服务，使用并行查询提高效率

- Fixed
  * 使用pathname和search替代href来跟踪页面URL

- Deprecated
  * 无

- Removed
  * 无

## [2025-10-30] - v1.0.5
- Added
  * 无

- Changed
  * 重构统计指标数据结构
  * 使用CTE优化SQL查询，一次性获取所有统计指标
  * 更新前端模板以显示新的指标和加载状态

- Fixed
  * 处理事件摘要查询中的空值，以确保在没有数据时返回默认值 0

- Deprecated
  * 无

- Removed
  * 无

## [2025-10-20] - v1.0.4
- Added
  * 无

- Changed
  * 优化数据库索引以提高查询性能
  * 改进了 dashboard 页面的数据加载方式，使用并发请求站点统计数据，提升页面加载速度

- Fixed
  * 无

- Deprecated
  * 无

- Removed
  * 无

## [2025-10-09] - v1.0.3
- Added
  * 新增基于 UserAgent 和分辨率的设备类型检测功能，用于更精确地检测设备类型

- Changed
  * 无

- Fixed
  * 修复 GetEventsRank 中 stat_type 和 event_type 的逻辑错误

- Deprecated
  * 无

- Removed
  * 无

## [2025-09-30] - v1.0.2
- Added
  * 新增 `daily_stats` 表，用于按天聚合各维度（OS、Browser、Device、Page、Referrer）的 PV 数据。
  * 实现批量 Upsert 功能，将事件数据高效写入 `daily_stats`。
  * 新增浏览器统计指标

- Changed
  * 统计逻辑优化：部分频繁查询直接从 `daily_stats` 表读取，减少事件表全表扫描，提高查询性能。
  * 浏览器和操作系统不显示版本号

- Fixed
  * 修复 Referrer 统计时子域名/路径分散问题，统一为主域名，空值归为“直接访问”。

- Deprecated
  * 无

- Removed
  * 无

## [2025-09-29] - v1.0.1
- 优化：
  - 统一指标命名以提高可读性
  - 实现 IP 匿名化

## [2025-09-29] - v1.0.0
- 初始版本发布：
  - 网站基础功能上线
  - API 功能完善

---

**感谢您选择 Pingoo 分析！我们致力于为您提供最佳的分析体验。如需更多帮助，请随时联系我们。**