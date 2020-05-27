import re
import logging
from database.db_operator import DbOperator
from ob import Observer
from my_timer import RepeatedTimer

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
ADMIN_LIST = ["ouwzNwvhpmyUVA8yGWtc0KF4yHks"]
class MainLogic(object):
    def __init__(self):
        self.db    = DbOperator()
        self.ob    = Observer()
        self.timer = RepeatedTimer(3600, self.ob.ob_all)
        self.cmd_list = {"help"        : self._help, 
                        "status"       : self._status, 
                        "list"         : self._list, 
                        "admin-status" : self._admin_status, 
                        "admin-list"   : self._admin_list}
        self.ob.init_checker()

    def __del__(self):
        self.timer.stop()

    def handle_msg(self, msg):
        print(msg.source, msg.content, msg.create_time) # tmp for debug
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

    def _handle_URL(self, msg):
        open_id = msg.source
        URL = msg.content

        success, result = self.db.find_my_article(open_id)
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
        success, _ = self.db.find_user(open_id)
        if not success:
            reply = reply + "\n--------------\n【警告】你的账号未绑定邮箱，无法收到备份及通知。请回复「help」查看规则"
        return reply

    def _handle_email(self, msg):
        open_id = msg.source
        email = msg.content
        user = (open_id, email)
        success, _ = self.db.find_user(open_id)
        if success: # 已有记录，更新绑定关系
            result = self.db.update_user(user)
        else: # 无记录，添加绑定关系
            result = self.db.add_user(user)
        if result:
            reply = "你的账号成功绑定邮箱：" + email
        else:
            # db 操作失败的日志 db 那边会记录
            reply = "绑定邮箱：" + email + "失败！请联系管理员处理：youdangls@gmail.com"
        return reply

    def _handle_cmd(self, msg):
        return self.cmd_list[msg.content](msg)

    def _help(self, msg):
        return "help命令暂不支持"

    def _status(self, msg):
        open_id = msg.source
        success, user = self.db.find_user(open_id)
        if not success:
            logger.info("_status can't find user:" + open_id)
            return "没有找到邮箱绑定记录，请重新绑定"
        email = user['email']
        return "你的账号现在绑定邮箱为：" + email

    def _list(self, msg):
        open_id = msg.source
        success, article_list = self.db.find_my_article(open_id)
        if not success:
            logger.info("_list no record by user:" + open_id)
            return "没有正在观察中的记录"
        reply = "现有" + str(len(article_list)) + "条记录观察中\n-------\n"
        URL_list = []
        for item in article_list:
            URL_list.append(item['URL'])
        reply = reply + "\n".join(URL_list)
        return reply

    def _admin_status(self, msg):
        user = msg.source
        if user not in ADMIN_LIST:
            return "非管理员账号，无法执行admin命令"
        success, result = self.db.fetch_all_article()
        if not success:
            logger.info("_admin_status fetch 0")
            return "admin-list 查询失败，结果为空"
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

    def _admin_list(self, msg):
        user = msg.source
        if user not in ADMIN_LIST:
            return "非管理员账号，无法执行admin命令"
        
        success, result = self.db.fetch_all_article()
        if not success:
            logger.info("_admin_list fetch 0")
            return "admin-list 查询失败，结果为空"

        output = []
        for item in result:
            output.append(item['article_id'])
            output.append(item['URL'])
            output.append(item['open_id'])
            output.append(item['backup_addr'])
            output.append(item['start_date'])
            output.append(item['status'])
            output.append('--------')
        return "\n".join(map(str, output))

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
        if string in self.cmd_list:
            return True
        else:
            return False


MAIN_LOGIC = MainLogic()

def get_main_logic():
    return MAIN_LOGIC