# do-ai Android App (React Native)

仅面向你个人使用的 Android 控制台：
- 拉取在线会话（默认在线过滤）
- 查看会话关键信息（host/cwd/cmd/last_text）
- 发送远程输入到指定会话（可选自动回车提交）
- 共享终端输出视图（tail 实时刷新）
- 向上加载历史输出（before 光标分页）
- 关键词本地通知（点通知自动跳到对应会话）

## 固定配置（已写死）

- Relay URL: `http://47.110.255.240:18787`
- Relay Token: `doai-relay-v1-9f8e7d6c5b4a3928171605ffeeddccbbaa99887766554433221100aabbccddeeff`

## 启动

```bash
cd app/android-rn
npm install
npm run android
```

> 需本机已安装 Android SDK / ADB / 模拟器。

## 接口

- `GET /api/v1/sessions`
- `POST /api/v1/control/send`
- `GET /api/v1/output/list`
- `POST /api/v1/output/push`（由 do-ai 客户端上报）
