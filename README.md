# Emberstripe 胶片光学声轨修复工位

Emberstripe 从胶片扫描工位交来的灰度 PNG 条带中读取双声道可变面积光学声轨。操作员可以核对灰阶标定、追踪任意画格对应的声槽、试听 PCM，并把音频和采样位置索引交给下一个修复工位。服务处理本机受信目录中的卷数据。

## 启动

需要 Go 1.23 或以上。服务和样例生成器使用 Go 标准库。复走脚本使用 Python 3.9 或以上的标准库。

```sh
go build ./...
go run ./cmd/samples -out var/scans
go run ./cmd/emberstripe -addr 127.0.0.1:7431 -scans var/scans -parcels var/parcels
```

样例生成器要求目标卷目录尚不存在。`cinder-a` 是 24 格/秒、每格 2000 行、声槽领先 230 行的右移取样案例；`vireo-b` 是 25 格/秒、每格 1920 行、偏移 −135 行的案例。`step-tablet` 是可重复的显影台阶段试条：14 个带已知相对强度的档，左右声槽曝光曲线不同，并故意安排了暗／亮平台、一个只有 16 有效行的档、一个全缺档和一对覆盖量相差 5.3 倍的相邻档。前两卷原生 PCM 采样率均为 48 kHz。`cinder-a` 与 `vireo-b` 含一张缺页和一张 8 位灰阶条带，其余条带是 16 位灰阶。`bench` 和 `soft` 是对比度不同的有效标定，`flat` 的参考窗口缺少对比度，`absent` 的暗场参考页缺失。

## 浏览器操作

启动后打开 `http://127.0.0.1:7431`。选择扫描卷、画格区间和光学标定，然后应用区间。点击扫描条带、波形或覆盖区间，让画格探针定位到同一行；也可以直接输入画格和行号。画面缺页标记在画格带上，偏移后的声槽缺口显示在波形中。

选择另一个可用标定后点击「比较」，可看到共同可读位置的左右声道差值。「载入试听」获取应用窗口的 WAV，试听控件与探针共享原生采样时钟。「冻结交接包」生成含 WAV 和画格索引的 ZIP，支持下载和再次校验。工作台静态资源随 Go 程序内嵌，无需前端构建或在线资源。

## 试条评估

打开 `http://127.0.0.1:7431/lightbench/step-tablet/tablet`（也可从 `step-tablet` 工作台顶部的「试条评估」进入）。页面按当前标定分别给出左、右两边的结论：

- **可确认范围**：相邻档逐对通过显著性、最小实用差值、有效行数和覆盖均衡检查后，连成的连续可辨区间。样例在 `bench` 标定下，左边可确认第 3–5、10–12 档，右边可确认第 4–5、10–12 档。
- **档级刻度**：每档展示已知相对强度刻线、标定实测均值条、灰阶均值与行波动、有效／声明行数，以及与下一档的判定（可辨、仍不可分、裁到平台、有效行太少、缺页无有效行、覆盖量悬殊）。
- **需要重扫／补页**：与可确认范围分开列出，给出涉及档和处理建议。裁到暗／亮平台建议调整曝光或参考端点，有效行不足或缺页建议补扫对应页，覆盖量相差 ≥4 倍的相邻档按覆盖混淆处理、需以相近覆盖重扫，不计入可确认范围。

评估是启动时卷快照摘要与标定的纯函数，重复提交结果一致；阈值（24 有效行、75% 削顶平台比、4 倍覆盖比、z≥2.5、0.03 最小标定差）在页面底部和 JSON 的 `thresholds` 中公开。

```sh
curl "$base/captures/step-tablet/tablet?profile=bench"
curl "$base/lightbench/step-tablet/tablet/evaluate?profile=bench"
```

没有 `tablet.steps` 定义的卷（如 `cinder-a`）该子资源返回 404。

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
curl "$base/captures/step-tablet/tablet?profile=bench"
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

独立单元与浏览器回归测试套件延后至后续任务；`internal/tablet` 含判定策略单元测试。上面的复走检查使用同一个 Go 服务，通过 HTTP 观察页面、WAV、PNG、JSON、ZIP 和失败状态，共十四条工作流（含 `tablet` 试条评估）。
