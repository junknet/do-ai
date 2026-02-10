#!/usr/bin/env python3
"""微信公众号文章发布脚本

用法：
    python publish.py article.md [--cover cover.jpg] [--author GeekPwd] [--draft-only]
    python publish.py --verify    # 仅验证 access_token

流程：
    1. 读取 Markdown 文件
    2. 转换为微信兼容 HTML
    3. 上传封面图
    4. 创建草稿
    5. 发布文章（除非 --draft-only）
"""

import argparse
import re
import sys
from pathlib import Path

import markdown

import wechat


def md_to_wx_html(md_text: str) -> str:
    """将 Markdown 转为微信公众号兼容的 HTML

    微信编辑器对 HTML 有诸多限制，这里做基本适配：
    - 使用内联样式（微信不支持 <style> 标签）
    - 代码块用灰底样式
    - 图片居中
    """
    html = markdown.markdown(
        md_text,
        extensions=["fenced_code", "tables", "nl2br"],
    )

    # 微信兼容样式包装
    # 段落样式
    html = html.replace("<p>", '<p style="margin: 1em 0; line-height: 1.8; font-size: 16px;">')

    # 标题样式
    for i in range(1, 5):
        html = html.replace(
            f"<h{i}>",
            f'<h{i} style="font-weight: bold; margin: 1.5em 0 0.8em; color: #1a1a1a;">',
        )

    # 代码块样式
    html = html.replace(
        "<code>",
        '<code style="background: #f5f5f5; padding: 2px 6px; border-radius: 3px; font-size: 14px;">',
    )
    html = html.replace(
        "<pre>",
        '<pre style="background: #2d2d2d; color: #f8f8f2; padding: 16px; border-radius: 8px; '
        'overflow-x: auto; font-size: 14px; line-height: 1.5;">',
    )

    # 列表样式
    html = html.replace("<ul>", '<ul style="margin: 1em 0; padding-left: 2em;">')
    html = html.replace("<ol>", '<ol style="margin: 1em 0; padding-left: 2em;">')
    html = html.replace("<li>", '<li style="margin: 0.5em 0; line-height: 1.8;">')

    # 引用块样式
    html = html.replace(
        "<blockquote>",
        '<blockquote style="border-left: 4px solid #42b983; padding: 12px 16px; '
        'margin: 1em 0; background: #f8f8f8; color: #666;">',
    )

    # 表格样式
    html = html.replace(
        "<table>",
        '<table style="border-collapse: collapse; width: 100%; margin: 1em 0;">',
    )
    html = html.replace(
        "<th>",
        '<th style="border: 1px solid #ddd; padding: 8px 12px; background: #f5f5f5; font-weight: bold;">',
    )
    html = re.sub(
        r"<td>",
        '<td style="border: 1px solid #ddd; padding: 8px 12px;">',
        html,
    )

    # 链接样式
    html = html.replace("<a ", '<a style="color: #42b983; text-decoration: none;" ')

    # 图片居中
    html = html.replace(
        "<img ",
        '<img style="max-width: 100%; display: block; margin: 1em auto;" ',
    )

    return html


def parse_frontmatter(text: str) -> tuple[dict, str]:
    """简单解析 YAML frontmatter（---包裹），返回 (meta, body)"""
    if not text.startswith("---"):
        return {}, text

    parts = text.split("---", 2)
    if len(parts) < 3:
        return {}, text

    meta = {}
    for line in parts[1].strip().splitlines():
        if ":" in line:
            key, val = line.split(":", 1)
            meta[key.strip()] = val.strip()

    return meta, parts[2].strip()


def do_verify():
    """验证 access_token 是否可用"""
    print("正在验证 access_token ...")
    try:
        token = wechat.get_access_token(force_refresh=True)
        print(f"  access_token 获取成功: {token[:20]}...")
        print("验证通过！")
    except Exception as e:
        print(f"验证失败: {e}")
        sys.exit(1)


def do_publish(md_path: str, cover_path: str | None, author: str, draft_only: bool):
    """完整发布流程"""
    md_file = Path(md_path)
    if not md_file.exists():
        print(f"文件不存在: {md_path}")
        sys.exit(1)

    # 1. 读取 Markdown
    print(f"[1/5] 读取文章: {md_file.name}")
    raw = md_file.read_text(encoding="utf-8")
    meta, body = parse_frontmatter(raw)

    title = meta.get("title", md_file.stem)
    digest = meta.get("digest", "")
    author = meta.get("author", author)

    if not cover_path:
        cover_path = meta.get("cover")

    print(f"  标题: {title}")
    print(f"  作者: {author}")

    # 2. Markdown → HTML
    print("[2/5] 转换 HTML ...")
    html_content = md_to_wx_html(body)
    print(f"  HTML 长度: {len(html_content)} 字符")

    # 3. 上传封面图
    print("[3/5] 上传封面图 ...")
    if not cover_path:
        # 使用默认封面
        default_cover = Path(__file__).parent / "default_cover.jpg"
        if not default_cover.exists():
            print("  错误: 未指定封面图，且 wx/default_cover.jpg 不存在")
            print("  请使用 --cover 参数指定封面图，或放置 wx/default_cover.jpg")
            sys.exit(1)
        cover_path = str(default_cover)

    thumb_media_id = wechat.upload_thumb_image(cover_path)

    # 4. 创建草稿
    print("[4/5] 创建草稿 ...")
    draft_media_id = wechat.create_draft(
        title=title,
        content=html_content,
        thumb_media_id=thumb_media_id,
        author=author,
        digest=digest,
    )

    if draft_only:
        print("\n已创建草稿（--draft-only 模式，不自动发布）")
        print(f"草稿 media_id: {draft_media_id}")
        print("请在微信公众号后台手动发布。")
        return

    # 5. 发布
    print("[5/5] 提交发布 ...")
    publish_id = wechat.publish(draft_media_id)

    print(f"\n发布完成！")
    print(f"  草稿 media_id:  {draft_media_id}")
    print(f"  发布 publish_id: {publish_id}")
    print("  注意: 发布为异步操作，可能需要几秒到几分钟生效。")


def main():
    parser = argparse.ArgumentParser(description="微信公众号文章发布工具")
    parser.add_argument("markdown", nargs="?", help="Markdown 文件路径")
    parser.add_argument("--cover", help="封面图路径（jpg，建议 900x383）")
    parser.add_argument("--author", default="GeekPwd", help="作者名（默认 GeekPwd）")
    parser.add_argument("--draft-only", action="store_true", help="仅创建草稿，不自动发布")
    parser.add_argument("--verify", action="store_true", help="仅验证 access_token")
    args = parser.parse_args()

    if args.verify:
        do_verify()
        return

    if not args.markdown:
        parser.error("请提供 Markdown 文件路径，或使用 --verify 验证凭据")

    do_publish(args.markdown, args.cover, args.author, args.draft_only)


if __name__ == "__main__":
    main()
