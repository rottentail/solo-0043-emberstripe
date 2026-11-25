# Emberstripe 胶片光学声轨修复工位

Emberstripe 从胶片扫描工位交来的灰度 PNG 条带中读取双声道可变面积光学声轨。操作员可以核对灰阶标定、追踪任意画格对应的声槽、试听 PCM，并把音频和采样位置索引交给下一个修复工位。服务处理本机受信目录中的卷数据。

## 启动

需要 Go 1.23 或以上。服务和样例生成器使用 Go 标准库。复走脚本使用 Python 3.9 或以上的标准库。

```sh
go build ./...
go run ./cmd/samples -out var/scans
go run ./cmd/emberstripe -addr 127.0.0.1:7431 -scans var/scans -parcels var/parcels
```

样例生成器要求目标卷目录尚不存在。`cinder-a` 是 24 格/秒、每格 2000 行、声槽领先 230 行的右移取样案例；`vireo-b` 是 25 格/秒、每格 1920 行、偏移 −135 行的案例。两卷原生 PCM 采样率均为 48 kHz。两卷均含一张缺页和一张 8 位灰阶条带，其余条带是 16 位灰阶。`bench` 和 `soft` 是对比度不同的有效标定，`flat` 的参考窗口缺少对比度，`absent` 的暗场参考页缺失。

## 浏览器操作

启动后打开 `http://127.0.0.1:7431`。选择扫描卷、画格区间和光学标定，然后应用区间。点击扫描条带、波形或覆盖区间，让画格探针定位到同一行；也可以直接输入画格和行号。画面缺页标记在画格带上，偏移后的声槽缺口显示在波形中。

选择另一个可用标定后点击「比较」，可看到共同可读位置的左右声道差值。「载入试听」获取应用窗口的 WAV，试听控件与探针共享原生采样时钟。「冻结交接包」生成含 WAV 和画格索引的 ZIP，支持下载和再次校验。工作台静态资源随 Go 程序内嵌，无需前端构建或在线资源。

## 两次扫描的撕边影响图

修复员在原扫上圈出撕边后，可用双方认定的参照点建立页号/行号变换。系统不会根据片孔重复纹自动配对；没有参照或落在参照包络外的部分只标为待确认。每条声槽/画面判断都带有所属卷、页号、扫描页摘要和覆盖状态，补扫页的 `proves_original_scan` 恒为 `false`，不能替原扫缺页补证。

```sh
# 机器 JSON 与可交给同事的 Markdown
go run ./cmd/impactmap \
  -scans var/scans \
  -survey examples/impact-survey.json \
  -out var/impact-map.json \
  -markdown var/impact-map.md

# 或通过服务（同时兼容 /impact、/impact-maps、/damage-map 等别名）
curl -X POST http://127.0.0.1:7431/impact-map \
  -H 'Content-Type: application/json' \
  --data @examples/impact-survey.json
```

浏览器入口为 `GET /lightbench/impact-map`。调查 JSON 的主结构是：

```json
{
  "original": "cinder-a",
  "rescan": "vireo-b",
  "references": [
    {"id": "k0", "original": {"page": 0, "row": 1000}, "rescan": {"page": 0, "row": 960}},
    {"id": "k2", "original": {"page": 2, "row": 1000}, "rescan": {"page": 2, "row": 960}}
  ],
  "marks": [
    {"id": "tear", "start": {"page": 1, "row": 1900}, "end": {"page": 2, "row": 120},
     "x0": 72, "x1": 82}
  ]
}
```

`start` 包含、`end` 排除，绝对扫描行为 `page × rows_per_frame + row`；一段可因此横跨接页。也可用 `page/start_row/end_row`、`zone: "picture"|"left"|"right"|"sound"|"edge"` 或 `lanes:["left"]`。输出在两卷上分别列出：

- `lane_hits`：实际碰到的左右声槽及列区间；
- `audible_frame_runs` / `audible_row_count`：按各卷自己的 `lead_rows` 反映射后的画格行，且声源页必须存在；
- `picture_runs` 与 `picture_only_runs`：画面受影响范围，以及扣除声槽损伤画格行后只剩画面受影响的范围；
- `corroborated_original_ranges` 与 `confirm_original_from`：参照和两卷页证能证实到哪里，从哪一原扫行起必须回原扫/原页确认；
- 每个 `evidence` 都可回查到具体卷、页、行段和该扫描页 SHA-256。

## 从画格检查到交付

在另一个终端运行以下命令。

```sh
base=http://127.0.0.1:7431
curl "$base/captures"
curl "$base/captures/cinder-a/header"
curl "$base/captures/cinder-a/calibration?profile=bench"
curl "$base/captures/cinder-a/register?frame=1&row=1770"
curl "$base/captures/cinder-a/coverage?start=1&end=4"
curl "$base/captures/cinder-a/trace?frame=0&row=400"
curl "$base/captures/cinder-a/compare?start=1&end=4&profile=bench&against=soft"
curl "$base/captures/cinder-a/overlay?frame=2&profile=bench" -o var/check.png
curl "$base/captures/cinder-a/audition?start=1&end=4&profile=soft" -o var/listen.wav
curl -X POST "$base/captures/cinder-a/release?start=1&end=4&profile=soft"
```

发布返回的 `Location` 可下载 ZIP，追加 `/verify` 得到完整性、时钟和覆盖检查结果。包内 `sound.wav` 是交错 16 位立体声，`register.json` 包含画格到包内 PCM 帧的索引，以及以卷内画格行为单位的实际声槽覆盖、标定值和字节摘要。索引里的画面摘要取自登记的扫描页，原图可向扫描工位调取。相同卷、画格窗和标定重复发布会得到同一标识。

`start` 包含起点、`end` 排除终点，二者都是零基画格号。`frame`、`row` 也是零基。以 `cinder-a` 为例，画格 1 的第 1770 行使用第 2 页的第 0 行声槽。第 2 页未扫描，试听 WAV 在相应位置保留双声道零值。区间有音频零值时，以 `coverage` 区分缺页、越出卷界和可读样本。`overlay` 左侧是该画格的画面灰度条带，右侧两个带分别表示映射到同一画格行的左／右声音。亮橙表示样本位置，亮红表示超出标定范围，暗红底表示该带没有输入材料。

输入格式、具体坐标规则和局部资源上限见 `PROJECT_SPEC.md`。真实工作流复走如下。默认运行时用临时目录及临时回环端口。

```sh
python3 examples/walkthrough.py all
python3 examples/walkthrough.py release
```

独立单元与浏览器回归测试套件延后至后续任务。上面的复走检查使用同一个 Go 服务，通过 HTTP 观察页面、WAV、PNG、JSON、ZIP 和失败状态。
