"""生成微信公众号封面图 900x383"""

from PIL import Image, ImageDraw, ImageFont

W, H = 900, 383
img = Image.new("RGB", (W, H), "#1a1a2e")
draw = ImageDraw.Draw(img)

# 渐变背景叠加
for y in range(H):
    r = int(26 + (16 - 26) * y / H)
    g = int(26 + (185 - 26) * y / H * 0.3)
    b = int(46 + (129 - 46) * y / H * 0.6)
    draw.line([(0, y), (W, y)], fill=(r, g, b))

# 装饰元素 - 终端风格的方块
for x in range(50, 850, 80):
    alpha = int(30 + (x % 160) / 160 * 40)
    draw.rectangle([x, 20, x + 40, 24], fill=(66, 185, 131, alpha))

for x in range(90, 850, 80):
    draw.rectangle([x, H - 24, x + 40, H - 20], fill=(187, 134, 252, 80))

# 终端光标装饰
draw.rectangle([60, 100, 68, 130], fill="#42b983")
draw.rectangle([60, 250, 68, 270], fill="#BB86FC")

# 字体
font_bold = ImageFont.truetype("/usr/share/fonts/noto-cjk/NotoSansCJK-Bold.ttc", 52)
font_sub = ImageFont.truetype("/usr/share/fonts/noto-cjk/NotoSansCJK-Regular.ttc", 26)
font_cmd = ImageFont.truetype("/usr/share/fonts/noto-cjk/NotoSansCJK-Medium.ttc", 22)

# 主标题
title = "do-ai · AI 监工"
bbox = draw.textbbox((0, 0), title, font=font_bold)
tw = bbox[2] - bbox[0]
draw.text(((W - tw) / 2, 110), title, fill="#ffffff", font=font_bold)

# 副标题
sub = "让 AI Agent 永不摸鱼"
bbox2 = draw.textbbox((0, 0), sub, font=font_sub)
tw2 = bbox2[2] - bbox2[0]
draw.text(((W - tw2) / 2, 185), sub, fill="#42b983", font=font_sub)

# 命令行示例
cmd = "$ do-ai claude  # 一行命令，解放双手"
bbox3 = draw.textbbox((0, 0), cmd, font=font_cmd)
tw3 = bbox3[2] - bbox3[0]
draw.text(((W - tw3) / 2, 260), cmd, fill="#aaaaaa", font=font_cmd)

# 底部标签
tag = "GeekPwd"
bbox4 = draw.textbbox((0, 0), tag, font=font_cmd)
tw4 = bbox4[2] - bbox4[0]
draw.text((W - tw4 - 40, H - 50), tag, fill="#666666", font=font_cmd)

img.save("/home/junknet/Desktop/do-ai/wx/default_cover.jpg", "JPEG", quality=95)
print("封面图已生成: wx/default_cover.jpg (900x383)")
