"""微信公众号 API 客户端

支持功能：
- access_token 获取（带文件缓存，2小时有效期）
- 上传图片素材（临时/永久）
- 创建草稿
- 发布文章（自由发布，无需群发）
"""

import json
import os
import time
from pathlib import Path

import requests
from dotenv import load_dotenv

# 加载 .env
_ENV_PATH = Path(__file__).parent / ".env"
load_dotenv(_ENV_PATH)

APP_ID = os.getenv("WECHAT_APP_ID")
APP_SECRET = os.getenv("WECHAT_APP_SECRET")

BASE_URL = "https://api.weixin.qq.com/cgi-bin"
TOKEN_CACHE = Path(__file__).parent / ".token_cache.json"


def _check_credentials():
    if not APP_ID or not APP_SECRET:
        raise ValueError("WECHAT_APP_ID 和 WECHAT_APP_SECRET 未配置，请检查 wx/.env")


def get_access_token(force_refresh=False) -> str:
    """获取 access_token，优先读缓存（2小时有效期内）"""
    _check_credentials()

    if not force_refresh and TOKEN_CACHE.exists():
        cache = json.loads(TOKEN_CACHE.read_text())
        if time.time() < cache.get("expires_at", 0):
            return cache["access_token"]

    resp = requests.get(f"{BASE_URL}/token", params={
        "grant_type": "client_credential",
        "appid": APP_ID,
        "secret": APP_SECRET,
    })
    data = resp.json()

    if "access_token" not in data:
        raise RuntimeError(f"获取 access_token 失败: {data}")

    cache = {
        "access_token": data["access_token"],
        "expires_at": time.time() + data.get("expires_in", 7200) - 300,  # 提前5分钟过期
    }
    TOKEN_CACHE.write_text(json.dumps(cache))
    return cache["access_token"]


def upload_image(image_path: str, permanent=False) -> str:
    """上传图片素材，返回 media_id

    Args:
        image_path: 图片文件路径
        permanent: True=永久素材, False=临时素材（3天有效）
    Returns:
        media_id
    """
    token = get_access_token()
    path = Path(image_path)
    if not path.exists():
        raise FileNotFoundError(f"图片不存在: {image_path}")

    if permanent:
        url = f"{BASE_URL}/material/add_material"
        params = {"access_token": token, "type": "image"}
    else:
        url = f"{BASE_URL}/media/upload"
        params = {"access_token": token, "type": "image"}

    with open(path, "rb") as f:
        resp = requests.post(url, params=params, files={"media": (path.name, f, "image/png")})

    data = resp.json()
    if "media_id" not in data:
        raise RuntimeError(f"上传图片失败: {data}")

    print(f"  图片已上传: media_id={data['media_id']}")
    return data["media_id"]


def upload_thumb_image(image_path: str) -> str:
    """上传封面图（thumb 类型，永久素材）

    微信草稿接口要求 thumb_media_id 必须是永久素材。
    """
    token = get_access_token()
    path = Path(image_path)
    if not path.exists():
        raise FileNotFoundError(f"图片不存在: {image_path}")

    url = f"{BASE_URL}/material/add_material"
    params = {"access_token": token, "type": "thumb"}

    with open(path, "rb") as f:
        resp = requests.post(url, params=params, files={"media": (path.name, f, "image/jpeg")})

    data = resp.json()
    if "media_id" not in data:
        raise RuntimeError(f"上传封面图失败: {data}")

    print(f"  封面图已上传: media_id={data['media_id']}")
    return data["media_id"]


def upload_content_image(image_path: str) -> str:
    """上传图文消息内的图片，返回 URL（用于文章正文内嵌图片）"""
    token = get_access_token()
    path = Path(image_path)
    if not path.exists():
        raise FileNotFoundError(f"图片不存在: {image_path}")

    url = f"{BASE_URL}/media/uploadimg"
    params = {"access_token": token}

    with open(path, "rb") as f:
        resp = requests.post(url, params=params, files={"media": (path.name, f, "image/png")})

    data = resp.json()
    if "url" not in data:
        raise RuntimeError(f"上传正文图片失败: {data}")

    print(f"  正文图片已上传: url={data['url']}")
    return data["url"]


def create_draft(title: str, content: str, thumb_media_id: str,
                 author: str = "GeekPwd", digest: str = "") -> str:
    """创建草稿，返回 media_id

    Args:
        title: 文章标题
        content: HTML 格式正文
        thumb_media_id: 封面图的 media_id（永久素材）
        author: 作者名
        digest: 摘要（为空则自动截取）
    Returns:
        草稿 media_id
    """
    token = get_access_token()

    article = {
        "title": title,
        "author": author,
        "content": content,
        "thumb_media_id": thumb_media_id,
        "need_open_comment": 0,
        "only_fans_can_comment": 0,
    }
    if digest:
        article["digest"] = digest

    payload = {"articles": [article]}

    resp = requests.post(
        f"{BASE_URL}/draft/add",
        params={"access_token": token},
        json=payload,
    )
    data = resp.json()

    if "media_id" not in data:
        raise RuntimeError(f"创建草稿失败: {data}")

    print(f"  草稿已创建: media_id={data['media_id']}")
    return data["media_id"]


def publish(media_id: str) -> str:
    """发布文章（自由发布）

    Args:
        media_id: 草稿的 media_id
    Returns:
        publish_id
    """
    token = get_access_token()

    resp = requests.post(
        f"{BASE_URL}/freepublish/submit",
        params={"access_token": token},
        json={"media_id": media_id},
    )
    data = resp.json()

    if data.get("errcode", 0) != 0:
        raise RuntimeError(f"发布失败: {data}")

    publish_id = data.get("publish_id", "unknown")
    print(f"  文章已提交发布: publish_id={publish_id}")
    return str(publish_id)


def get_publish_status(publish_id: str) -> dict:
    """查询发布状态"""
    token = get_access_token()

    resp = requests.post(
        f"{BASE_URL}/freepublish/get",
        params={"access_token": token},
        json={"publish_id": publish_id},
    )
    return resp.json()
