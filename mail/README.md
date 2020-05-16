## mail 服务搭建经验

### 安装、配置sendmail
python 的 smtplib 依赖于系统的 sendmail 服务。若服务器上没有该程序，

#### 先安装：
    sudo apt-get install sendmail

查看是否安装成功

    ps aux | grep sendmail

会出现类似信息：

```shell
root     14264  0.0  0.5 100700  2788 ?        Ss   14:43   0:00 sendmail: MTA: accepting connections
root     14602  0.0  0.1  11740   940 pts/1    S+   15:29   0:00 grep --color=auto sendmail
```
#### 配置：
sendmail 默认是本机用户发送给本机，所以需要修改为可以发送到整个 Internet：

修改 sendmail 配置宏文件，路径为 `/etc/mail/sendmail.mc`

找到：

    DAEMON_OPTIONS(`Family=inet,  Name=MTA-v4, Port=smtp, Addr=127.0.0.1')dnl

将`Addr=127.0.0.1`修改为`Addr=0.0.0.0`，意思是可以连接到任何服务器。

保存修改的文件，把旧配置文件备份一下：

```shell
cd /etc/mail
mv sendmail.cf sendmail.cf~
```

然后生成新的配置文件：

    m4 sendmail.mc > sendmail.cf

接下来修改hosts文件，路径为`/etc/hosts`

原内容大概为：
```
127.0.1.1 name name
127.0.0.1 localhost
```

修改为：

    your.IP.XX.XX  your.domainname

保存并关闭文件。

重启 sendmail 服务：

    service sendmail restart

就可以通过 sendmail 发送邮件了。

以上所有内容参考：[Ubuntu:使用sendmail配置邮件服务，发送邮件](https://www.polarxiong.com/archives/ubuntu-sendmail.html)

（但是不要按它所说的去做邮件测试。会直接导致 gmail 封禁你的 IP ……）

*******

### 进一步配置（smtp、服务器验证等）
前面的步骤已经实现了mail功能的部署。

若要结合python、smtp使用，在python中调用 smtplib 库即可。

smtplib的使用参见：[python - 如何通过GMail发送邮件：smtplib的使用](https://blog.csdn.net/leehark/article/details/7173570)

但直接发送邮件会**被大多数邮箱服务商判断为 spam server 直接封禁**。

为此进行进一步配置：

#### 修改主机名（影响了发件方的域名后缀）

修改hostname文件（路径：`/etc/hostname`）：

    $ sudo vi /etc/hostname

把hostname文件里面所有原来的名称改成你的域名。

主机名同时也保存在`/etc/hosts`文件中，需要把当前IP地址对应的主机名修改为 hostname 文件中的名称。但你如果已经在前一步 sendmail 部分里面改过，就不用再改

    $ sudo vi /etc/hosts

最后重启机器：

    $ reboot


#### 使用 gmail smtp 服务发邮件
gmail 对于认证和验证要求比较严，如果验证失败，可能是：
* Google阻止用户使用不符合他们安全标准的应用或设备登陆gmail
* Gmail没有解除验证码认证

解决方案参见：[解决python使用gmail smtp服务发邮件报错smtplib.smtpauthentic](https://blog.csdn.net/qianghaohao/article/details/79331895)






#### 其他
gmail 官方文档：
* [借助 SPF 记录防范电子邮件假冒行为](https://support.google.com/a/answer/33786)
* [关于 SMTP 错误消息](https://support.google.com/a/answer/3221692)
* [SMTP 错误参考](https://support.google.com/a/answer/3726730)

python 调试 smtplib 库时，`smtplib.SMTP()`对象有一个方法是`set_debuglevel()`。
调用这个方法，可以输出 debug 信息到 terminal：

    smtp.set_debuglevel(2) # 参数 1 或 True 代表输出debug信息， 2 代表输出debug信息并打上时间戳

查看邮件队列的命令：

    $ mailq


