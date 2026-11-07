---
name: engineering-drawing-balloon
description: 为工程图 PDF 识别检验特性，按视图分块编号，生成蓝色气泡标注 PDF、Excel/CSV 检验表和审核报告。用于尺寸气泡编号、FAI 首件检验图纸标注、检验特性提取。Balloon engineering drawing PDFs for first article inspection.
---

# 工程图气泡标注

本目录包含引擎、样式、示例和测试。所有路径相对于本文件所在目录，下面用 SKILL_DIR 表示其绝对路径。

## 环境

需要 Python 3.10+。首次使用运行一次安装脚本，它在 SKILL_DIR/.venv 建立虚拟环境并安装锁定依赖；已安装时直接复用。

```bash
bash "$SKILL_DIR/scripts/setup.sh"
```

安装失败时报告具体错误（Python 版本、网络、pip），不要改用系统 Python 或升级依赖版本。

## 执行流程

1. 核对用户输入 PDF 路径和独立输出目录（不得与 PDF 同目录）。检查 PDF 完整、有页面、无需密码。保留原图。
2. 逐页渲染并查看图纸，提取原生文字。扫描图、转曲文字或混合页面需要 AI 视觉识别补齐特性：尺寸、公差、孔组数量、螺纹、形位公差、粗糙度、棱边要求和检验相关注释。保留原文，无法确认的内容列入审核报告。坐标原点 0、基准符号、标题栏和 "see 3D" 类注释不编号。
   - 密集区域请局部放大并叠加坐标网格读取 bbox，避免估算误差。
3. 为当前图纸生成 features.json。坐标使用 PDF point，左上角为原点，页码从 1 开始。若渲染倍率为 s，图像坐标除以 s，正确处理页面旋转。bbox 必须包围整条特性文字及其公差和符号。

```json
{"view_order": ["主视图", "侧视图", "A (1:2)", "技术要求"],
 "features": [{"page": 1, "text": "124,5", "bbox": [130, 135, 162, 150],
               "kind": "dimension", "source": "ai_ocr", "view": "主视图"}],
 "keep_out": [{"page": 1, "bbox": [333, 421, 526, 562]}]}
```

   - view：特性所属视图（主视图、侧视图、局部视图 A、技术要求等），每个特性都应填写。编号按视图分块、块间连续。
   - view_order：可选，显式指定视图先后；未列出的视图按其左上角位置从左上到右下排在后面。
   - keep_out：可选，禁止放置气泡的区域（引线仍可穿过），一般不需要。
   - 坐标尺寸可在 text 前加方向前缀，如 "X 53"、"Y 72,5"，便于检验表阅读。
   - 完整示例见 examples/demo_features.json。

4. 运行引擎。

```bash
"$SKILL_DIR/.venv/bin/python" "$SKILL_DIR/scripts/balloon.py" \
  --input "/任务目录/原图.pdf" \
  --features "/任务目录/features.json" \
  --style "$SKILL_DIR/style.json" \
  --output-dir "/任务目录/output" --ocr off
```

   引擎行为：
   - 引线默认为楔形（气泡边两点收拢成尖点指向特性，style.json 中 leader_style="wedge"、wedge_width 调宽度）；设为 "arrow" 则为直线 + 实心箭头。
   - 避让文字框、PDF 矢量线条（图框、轮廓、尺寸线、转曲文字）和像素墨迹；引线穿线越少越优先。
   - 共边对齐且比气泡更密的特性组（如坐标尺寸）自动识别，在自由端外侧交错多排排布。
   - 编号：视图块按 view_order 或左上到右下排序；块内按阅读顺序（同一行从左到右），密排组整组连续编号；跨块、跨页连续。未填 view 时整页视为一块。
   - 原生文字提取充分时可省略 --features。没有文字层且未提供特性时，引擎输出 page_N_NEEDS_AI_OCR.png 预览，AI 应查看并生成 features.json 后重跑。
   - 可选 --reference 参考图仅在 style.json 未设置 color 时用于取色。scripts/ocr_adapter.py 提供可选 RapidOCR 接口（--ocr auto），其依赖和模型未打包。

5. 逐页渲染最终 PDF 并查看，核查特性遗漏、文字遮挡、气泡压线、引线交叉、箭头指向、编号顺序和对应关系。退出码 0 不代表识别和排版全部合格。个别气泡放不下时先修正 bbox 或加 keep_out 后重跑，仍不行则列入审核报告。
6. 交付 *_ballooned.pdf、*_inspection.xlsx、*_inspection.csv 和 *_review.json。报告识别数量、已编号数量、各视图编号范围、待复核项和图纸注明需另查 3D 的尺寸。给出实际可用的文件路径。

退出码：0 布局完成但仍需人工审核；2 输入或运行错误；3 识别或放置不完整，查看 *_review.json。

## 验证

```bash
"$SKILL_DIR/.venv/bin/python" "$SKILL_DIR/tests/test_arrow_layout.py"
"$SKILL_DIR/.venv/bin/python" "$SKILL_DIR/tests/smoke_test.py"
"$SKILL_DIR/.venv/bin/python" "$SKILL_DIR/scripts/balloon.py" --input "$SKILL_DIR/examples/demo_drawing.pdf" \
  --features "$SKILL_DIR/examples/demo_features.json" --style "$SKILL_DIR/style.json" \
  --output-dir /tmp/engineering-drawing-balloon-demo --ocr off
```

保持用户输入和本目录文件原样；用户明确要求时才修改引擎或样式。
