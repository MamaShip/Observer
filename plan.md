
![简易框架](./frame.png ''简易框架'')

### Msg_Handler

使用微信提供的公众号API处理微信消息。具体用法待研究。

可参考的SDK库：
* Python SDK：https://github.com/wechatpy/wechatpy
* Go SDK：https://github.com/chanxuehong/wechat


### 主逻辑

涉及以下功能：
* 用户初次绑定邮箱
* 用户修改绑定的邮箱
* 新文章加入观察
* 立即/周期性触发观察者的观察动作


### Observer

功能：
调用 DB Operator 读取观察目标，执行一次观察：
* 若为初次观察，做一次备份
* 若链接有效，且存在备份，结束
* 若链接失效，向数据库写入失效标签，发送备份给用户，从观察列表中剔除。

### Mail_Server

发送备份的邮件给特定邮箱



