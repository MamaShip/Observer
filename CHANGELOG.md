
<a name="Unreleased"></a>
## [Unreleased](https://github.com/MamaShip/Observer/compare/alpha...Unreleased) (2020-06-06)

### Bug Fixes

* 修正dev分支不能触发CI的bug

### Continuous Integration

* 添加自动生成 change log 的设定

### Documents

* 指南页添加订阅号星标指引

### Features

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

