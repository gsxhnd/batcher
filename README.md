# batcher

批处理工具，提供交互式终端界面（TUI）和桌面图形界面（GUI，基于 [Wails v3](https://v3.wails.io)）。

## 目录结构

```text
cmd/
  batcher/            TUI 入口
  gui/                GUI 入口（Wails v3）
    main.go           应用与窗口
    service.go        暴露给前端的 BatchService（只做转发，不含业务逻辑）
    frontend/         Vite + TypeScript 前端，bindings/ 为自动生成
    build/            各平台打包资源
    Taskfile.yml      GUI 构建任务
internal/
  ffmpeg/             核心逻辑：命令生成、并发执行、进度事件（与 TUI/GUI 无关）
  rename/             文件重命名核心逻辑
  tui/                bubbletea 交互式界面
  utils/              通用工具
```

TUI 和 GUI 都只调用 `internal/` 下的核心包。执行进度通过 `ffmpeg.Event` 回调传出：TUI 渲染进度条与日志，GUI 则转成 Wails 事件推给前端。

## TUI

基于 [Bubble Tea v2](https://github.com/charmbracelet/bubbletea) 的交互式终端界面，支持视频转码、添加字幕、添加字体的任意组合，一次生成并执行同一条 ffmpeg 命令：

```bash
go run ./cmd/batcher
```

界面流程：

1. `space` 勾选操作（视频转码 / 添加字幕 / 添加字体，至少一项）。
2. 勾选了转码 → 转码参数页：选择常用预设（`Enter` 使用）、`d` 删除预设，或选择"(手动输入参数)"进入表单手填；在表单页的"高级自定义参数"里按 `ctrl+s` 可把当前参数保存为预设，持久化到 `~/.batcher/advance_presets.json`。预设留空则仅按输出后缀转换。
3. 勾选了字幕 → 字幕列表页：`a` 新增、`e` 编辑、`d` 删除，支持多条字幕；每条字幕可指定后缀、语言缩写（ISO 639-2 三字母码，如 `chi` / `eng` / `jpn`）、轨道标题。字幕文件按 `<视频名>.<后缀>` 或 `<视频名>.<标记>.<后缀>` 匹配（如 `1.mp4` 对应 `1.ass`、`1.chi.ass`、`1.eng.srt`；同一后缀有多条时用带标记的后缀管理，例如后缀填 `chi.ass` / `eng.ass`）。多个字幕流序号从 `input_sub_no` 起自动递增。
4. 主参数表单 → `Enter` 生成命令预览（相当于内置 dry-run，不会运行命令）→ `Enter` 开始执行，带实时进度条与 ffmpeg 输出日志。

组合逻辑：命令为 `ffmpeg -i <视频> [高级参数] [字幕输入×N] -map 0 -map 1 … -metadata:s:s:0 language=chi … [字体参数] [未开启转码时加 -c copy] <输出>`——转码 + 多条字幕 + 字体在同一条命令、同一个进程内完成，只处理一遍。

按键：`q`/`Ctrl+C` 退出；执行中 `q`/`esc` 中止批处理；结果页 `r` 重新执行、`b` 返回参数配置。

## GUI

需要 Go 1.26+、Node.js，以及 `wails3` CLI：

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0

cd cmd/gui
wails3 dev      # 开发模式（热更新）
wails3 build    # 构建，产物在 cmd/gui/bin/
wails3 package  # 打包（macOS .app / Windows 安装包 / Linux 包）
```

修改了 `cmd/gui/service.go` 的方法签名后，`wails3 build` / `wails3 dev` 会自动重新生成前端 bindings。

## 测试

```bash
mkdir -p testdata/{1,2} && touch testdata/{1,2}/{1,2}.{mkv,mp4,ass,ttf}
go test ./internal/... ./cmd/batcher/...
```
