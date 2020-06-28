
<a name="Beta"></a>
## [Beta](https://github.com/MamaShip/Observer/compare/alpha...Beta) (2020-06-19)

### Bug Fixes

* 发送回复前判断消息长度，超长时截断，避免用户端显示故障
* 修正list命令输出方式，保证向后兼容
* 解决存储文章标题后备份功能异常的问题
* 返回用户输入时先执行转义，防止跨站脚本攻击
* 通知邮件内现在可以正确显示停止原因
* 用户收到的备份内容缺失
* 修正dev分支不能触发CI的bug

### Continuous Integration

* 添加自动生成 change log 的设定

### Documents

* 指南页添加订阅号星标指引

### Features

* mail 寄送改由独立线程处理
* 完善备份流程，用户现在可以看到备份文章标题了
* 临时备份文件存至单独的路径，不与代码混在一起
* 数据库现在存储了文章标题
* 用户初次绑定邮箱时会收到通知邮件
* 管理员命令功能增强
* 完善文章状态的定义，在文章失效时根据失效原因记录不同状态
* 允许用户发送article_id删除观察目标
* 修改命令识别方式：不区分大小写，允许接受带参数的命令

### BREAKING CHANGE


当文章停止观察时必须填写原因，否则会触发状态更新异常


<a name="alpha"></a>
## alpha (2020-06-01)

### Code Refactoring

* move db function to db_operator
* 把事件处理搬移到主逻辑

### Documents

* update .all-contributorsrc [skip ci]
* update README.md [skip ci]
* update .all-contributorsrc [skip ci]
* update README.md [skip ci]
* update .all-contributorsrc [skip ci]
* update README.md [skip ci]
* update .all-contributorsrc [skip ci]
* update README.md [skip ci]
* update .all-contributorsrc [skip ci]
* update README.md [skip ci]
* create .all-contributorsrc [skip ci]
* update README.md [skip ci]

