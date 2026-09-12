# Web 前端架构选型

## 概述

基于 React + TypeScript 构建的前端应用。

## 技术选型

### 核心框架

**UI 框架：React**

- 组件化：声明式 UI，组件复用
- 生态丰富：社区活跃，第三方库多
- 虚拟 DOM：高性能渲染

**语言：TypeScript**

- 类型安全：编译时类型检查
- 代码提示：IDE 支持完善
- 可维护性：大型项目必备

**AI 聊天：Vercel AI SDK (`@ai-sdk/react` + `streamdown`)**

- 流式聊天：useChat hooks，自动处理 SSE
- 消息状态：streaming / ready / error
- Markdown 渲染：streamdown，支持代码高亮、数学公式、Mermaid 图表
- 功能完整：重试、停止、工具调用状态

**UI 组件库：Shadcn/ui + Aceternity UI**

- Shadcn/ui：基础 UI 组件，稳定可靠
- Aceternity UI：炫酷效果（玻璃态、悬浮标签、3D 卡片）
- 两者配合：既有基础组件，又有现代化效果

**样式方案：Tailwind CSS**

- 原子化 CSS：灵活组合
- 与 Shadcn/ui / Aceternity UI 深度集成
- 支持玻璃态效果（backdrop-blur、半透明背景）

**动画引擎：Framer Motion**

- 流畅动画：硬件加速
- 与 Aceternity UI 配合使用

**构建工具：Vite**

- 快速：ESBuild 预构建
- 轻量：配置简单

**状态管理：Zustand**

- 轻量：API 简单，无样板代码
- 灵活：支持中间件

**HTTP 请求：Axios**

- 通用：支持拦截器、取消请求
- 稳定：社区广泛使用

**路由：React Router**

- 官方方案：React 生态标准

**表单：React Hook Form**

- 高性能：非受控组件
- 简单：API 直观

## 目录结构（待定）

```
web/
├── src/
│   ├── components/     # 通用组件
│   ├── pages/          # 页面组件
│   ├── hooks/          # 自定义 Hooks
│   ├── services/       # API 请求
│   ├── stores/         # 状态管理
│   ├── types/          # TypeScript 类型
│   ├── utils/          # 工具函数
│   └── styles/         # 样式文件
├── public/
├── package.json
└── tsconfig.json
```
