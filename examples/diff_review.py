#!/usr/bin/env python3
"""离线差异听审件入口：对照两个已校验的冻结交接包。

这是 `cmd/diffreview` 的标准库启动器（与 examples/walkthrough.py 一样先
`go build` 再运行）。所有参数原样转发：

    python3 examples/diff_review.py \\
        -a var/parcels/<id-a>.zip -b var/parcels/<id-b>.zip \\
        -out var/diff-review

    # 或用归档 store 里的包 ID：
    python3 examples/diff_review.py \\
        -a-id <sha256-a> -b-id <sha256-b> -parcels var/parcels

    # 生成覆盖三种标记的演示包并出件：
    python3 examples/diff_review.py demo -out var/diff-demo

产物（写入 -out 目录）：
    diff-review.json   机器可读报告（出处、逐画格标记、证据、双包 cue）
    diff-review.md     中文剪辑台报告
    diff-review.html   离线可开的听审页（对照音频内嵌）
    review.wav         全部差异区域连播（A→间隔→B）
    clips/region-*.wav 每区 A/B 原速摘录与共同采样率对照片段
    diff-review.zip    上述文件的打包听审件（附 .sha256）
"""
import os
import subprocess
import sys
import tempfile
from pathlib import Path


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    go = os.environ.get("GO", "go")
    with tempfile.TemporaryDirectory(prefix="diffreview-") as tmp:
        binary = Path(tmp) / "diffreview"
        build = subprocess.run(
            [go, "build", "-o", str(binary), "./cmd/diffreview"],
            cwd=root,
            capture_output=True,
            text=True,
        )
        if build.returncode != 0:
            sys.stderr.write(build.stderr or "go build failed\n")
            return 1
        if not binary.exists():
            sys.stderr.write("error: diffreview binary was not built\n")
            return 1
        return subprocess.run([str(binary), *sys.argv[1:]], cwd=root).returncode


if __name__ == "__main__":
    raise SystemExit(main())
