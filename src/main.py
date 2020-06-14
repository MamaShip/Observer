import re
import logging
from database.db_operator import DbOperator
from observer import Observer, update_article_status, DEFAULT_PATH, send_user_check_email
from my_timer import RepeatedTimer
from definitions import REASON_DELETE_BY_USER
from utils.tools import total_used_space

#先声明一个 Logger 对象
logger = logging.getLogger("main")
logger.setLevel(level=logging.DEBUG)
#然后指定其对应的 Handler 为 FileHandler 对象
handler = logging.FileHandler('sys.log')
#然后 Handler 对象单独指定了 Formatter 对象单独配置输出格式
formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
handler.setFormatter(formatter)
logger.addHandler(handler)

EMAIL_RULE = re.compile(r'^[a-zA-Z0-9\._\-\+]{1,64}@([A-Za-z0-9_\-\.]){1,128}\.([A-Za-z]{2,8})$')
# CMD_LIST = ["help", "status", "list", "admin-status", "admin-list"]
ADMIN_LIST = ["ouwzNwvhpmyUVA8yGWtc0KF4yHks",
              "ouwzNwiEzAQFZfIluN4j2fQ2-2x4"]
HELP_MSG = """初次使用请直接发送邮箱地址进行关系绑定。
此后直接发送微信公众号文章地址即可启动观察。
-------------------
也可直接发送查询命令：
[status]  - 查看邮箱绑定状态
[list]    - 查看正在观察的文章列表
更多信息参见： http://wx.twisted-meadows.com
"""
class MainLogic(object):
    _instance = None

    def __new__(cls, *args, **kwargs):
        if not cls._instance:
            cls._instance = object.__new__(cls, *args, **kwargs)
        return cls._instance

    def __init__(self):
        self.ob    = Observer()
        self.timer = RepeatedTimer(14400, self.ob.ob_all)
        self.cmd_list = {"help"        : self._help, 
                        "status"       : self._status, 
                        "list"         : self._list, 
                        "delete"       : self._delete,
                        "admin-status" : self._admin_status, 
                        "admin-list"   : self._admin_list,
                        "admin-run"    : self._admin_run}
        self.ob.init_checker()

    def __del__(self):
        self.timer.stop()

    def handle_msg(self, msg):
        # print(msg.source, msg.content, msg.create_time)
        content = msg.content
        reply = ""

        if self._is_URL(content):
            reply = self._handle_URL(msg)
        elif self._is_email(content):
            reply = self._handle_email(msg)
        elif self._is_cmd(content):
            reply = self._handle_cmd(msg)
        else: # invalid msg
            reply = "听不懂你在说啥！\n--------------\n观察目标暂时只接受微信公众号文章。\n不要有无意义的空格、分号、换行符等。\n请回复「help」查看规则"
        
        return reply
    
    def handle_event(self, evt):
        if self._is_subscribe_event(evt):
            reply = "欢迎关注，回复[help]查看基础指引。\n机器人功能仍在施工中，见谅"
        else:
            reply = "Sorry, can not handle this for now"
        return reply

    def _handle_URL(self, msg):
        open_id = msg.source
        URL = self._trim_URL(msg.content)

        db = DbOperator()
        success, result = db.find_my_article(open_id)
        if success:
            for item in result:
                if URL == item['URL']:
                    reply = "目标已在观察列表中：" + URL
                    return reply
        
        success = self.ob.ob_this_one(URL, open_id)
        if not success:
            reply = "目标初次访问异常，无法进行备份。若确定是误判，请联系管理员：youdangls@gmail.com"
        else:
            reply = "收到！开始观察目标！"
        # 判断一下用户是否绑定邮箱，没绑定的话提供警告
        success, _ = db.find_user(open_id)
        if not success:
            reply = reply + "\n--------------\n【警告】你的账号未绑定邮箱，无法收到备份及通知。请回复[help]查看规则"
        return reply

    def _handle_email(self, msg):
        open_id = msg.source
        email = msg.content
        user = (open_id, email)
        send_flag = False
        db = DbOperator()
        success, _ = db.find_user(open_id)
        if success: # 已有记录，更新绑定关系
            result = db.update_user(user)
        else: # 无记录，添加绑定关系
            result = db.add_user(user)
            send_flag = send_user_check_email(email)
        if result:
            reply = "你的账号成功绑定邮箱：" + email
            if send_flag:
                reply += "\n--------\n"
                reply += """你是初次绑定邮箱，我发送了一封确认邮件给您
                            请检查邮箱,确认能收到我的通知邮件
                            （注意垃圾箱！）"""
        else:
            # db 操作失败的日志 db 那边会记录
            reply = "绑定邮箱：" + email + "失败！请联系管理员处理：youdangls@gmail.com"
        return reply

    def _handle_cmd(self, msg):
        reply = "处理命令出错，无法提取出有效命令"
        string = msg.content
        try:
            argv = string.split()
            cmd = argv[0].lower()
        except IndexError:
            logger.warning("_handle_cmd parse string fail: " + string)
            return reply
        try:
            reply = self.cmd_list[cmd](msg)
        finally:
            return reply

    def _help(self, msg):
        return HELP_MSG

    def _status(self, msg):
        open_id = msg.source
        db = DbOperator()
        success, user = db.find_user(open_id)
        if not success:
            logger.info("_status can't find user:" + open_id)
            return "没有找到邮箱绑定记录，请重新绑定"
        email = user['email']
        return "你的账号现在绑定邮箱为：" + email

    def _list(self, msg):
        open_id = msg.source
        db = DbOperator()
        success, article_list = db.find_my_article(open_id)
        if not success:
            logger.info("_list no record by user:" + open_id)
            return "没有正在观察中的记录"
        reply = "现有" + str(len(article_list)) + "条记录观察中\n-------\n"
        URL_list = []
        for item in article_list:
            URL_list.append(str(item['article_id']) + " " + item['URL']
                            + " " + item['title'])
        reply = reply + "\n\n".join(URL_list)
        return reply

    def _admin_status(self, msg):
        user = msg.source
        if user not in ADMIN_LIST:
            return "非管理员账号，无法执行admin命令"

        db = DbOperator()
        reply = analyze_article_status(db)
        reply += "\n-------\n"
        reply += analyze_user_status(db)
        reply += "\n-------\n"
        qsize = self.ob.get_cur_q_size()
        reply += "队列中的条目数量：" + str(qsize)
        reply += "\n-------\n"
        space_info = total_used_space(DEFAULT_PATH)
        reply += space_info
        return reply

    def _admin_list(self, msg):
        user = msg.source
        if user not in ADMIN_LIST:
            return "非管理员账号，无法执行admin命令"
        db = DbOperator()
        success, result = db.fetch_all_article()
        if not success:
            logger.info("_admin_list fetch 0")
            return "admin-list 查询失败，结果为空"

        output = []
        for item in result:
            output.append(item['article_id'])
            output.append(item['URL'])
            output.append(item['open_id'])
            output.append(item['backup_addr'])
            # output.append(item['start_date']) # 这个有bug，先注释掉
            output.append(item['status'])
            output.append('--------')
        return "\n".join(map(str, output))

    def _admin_run(self, msg):
        user = msg.source
        if user not in ADMIN_LIST:
            return "非管理员账号，无法执行admin命令"
        if self.ob.ob_all():
            return "已执行一次全局观察"
        else:
            return "全局观察执行中出错，请检查日志"

    def _delete(self, msg):
        string = msg.content
        try:
            argv = string.split()
            article_id = int(argv[1])
        except IndexError:
            logger.exception("_delete parse string fail: " + string)
            return "delete 失败，不是合法的输入，请检查格式"
        
        db = DbOperator()
        success, result = db.find_article(article_id)
        if not success:
            logger.debug("_delete fetch nothing: " + string)
            return "试图删除编号为" + str(article_id) + "的文章，但未找到"
        user = msg.source
        if result['open_id'] == user: # 确实是本人的观察目标
            if update_article_status(article_id, False, optionals={"reason": REASON_DELETE_BY_USER}):
                return "成功删除观察目标：" + str(article_id)
            else:
                logger.error("_delete process fail: " + string)
                return "您的输入合法，但后台处理失败，请联系管理员"
        else:
            logger.info(user + " try to delete article: " + str(article_id))
            return str(article_id) + " 不是您的观察目标，无法删除"

    def _is_URL(self, string):
        # 特殊处理，暂时只接受微信文章地址
        if string.startswith('https://mp.weixin.qq.com/s'):
            #合法性校验
            if (' ' not in string) and (len(string) < 2000):
                return True
        return False

    def _is_email(self, string):
        return EMAIL_RULE.match(string)

    def _is_cmd(self, string):
        try:
            argv = string.split()
            header = argv[0].lower()
        except IndexError:
            return False

        if header in self.cmd_list:
            return True
        else:
            return False

    def _trim_URL(self, URL):
        if "&chksm=" in URL:
            try:
                short_ver = URL.split("&chksm=")[0]
            except IndexError:
                logger.warning("_trim_URL fail: " + URL)
                return URL
            else:
                return short_ver
        else:
            return URL
    
    def _is_subscribe_event(self, msg):
        if msg.event == "subscribe":
            return True
        return False

def analyze_article_status(db):
    success, result = db.fetch_all_article()
    if not success:
        logger.info("analyze_article_status fetch 0 article")
    cnt = len(result)
    reply = "现有" + str(cnt) + "条记录观察中"
    cnt_dict = {}
    for item in result:
        status = item['status']
        if status not in cnt_dict:
            cnt_dict[status] = 1
        else:
            cnt_dict[status] += 1
    for i in cnt_dict:
        reply = reply + "\n状态" + str(i) + "的有" + str(cnt_dict[i]) + "条"
    return reply

def analyze_user_status(db):
    _, result = db.fetch_all_user()
    cnt = len(result)
    return "有" + str(cnt) + "名用户已绑定邮箱"

if __name__ == "__main__":
    print("test handle cmd")
    main = MainLogic()
    print(main._is_cmd("stAtus 14"))