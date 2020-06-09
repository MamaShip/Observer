import os
import logging
import datetime
import shutil
from article_checker.article_checker import Checker_Queue, Article_Checker
from database.db_operator import DbOperator
from mail.mail import send_mail
from definitions import *

# 获取 Logger 对象(跟main共用一个logger)
logger = logging.getLogger("main")

DEFAULT_PATH = "/var/wx/article"
FAKE_PATH_PLACE_HOLDER = "placeholder"
MAX_OB_DAYS = 30

def notify_user(email, URL, backup_addr, reason=REASON_NULL):
    """Send email to users.
    Send email to users for notifying article expiration.
    With backup file attached.

    Args:
        URL : str
        backup_addr : str
        reason : str
            must be keys in the dict {reason2text}

    Returns:
        success: bool
    """
    receiver = [email]
    try:
        reason_text = reason2text[reason]
    except KeyError:
        logger.exception("notify_user reason key ERROR")
        reason_text = "停止观察的原因未知"

    if backup_addr is None:
        addition = '\n截止观察结束时，没有成功备份的文档存留。\n如有疑问请联系管理员：youdangls@gmail.com'
        attach = []
    else:
        addition = '\n附件是文章备份，请查收。'
        attach = [backup_addr]
    body_text = '您观察的文章：' + URL + reason_text + addition
    msg = {'Subject': '您的观察目标有状态更新', 'Body': body_text}
    return send_mail(receiver, contents=msg, attachments=attach)


def update_article_status(article_id, valid, backup_path=None, optionals={}):
    """Do jobs when article status changed.

    Args:
        article_id : int
        valid : bool
        backup_path : str
        optionals : dict 
            maybe contains: "reason" shows why status changed
                            "title" shows article title detected
    Returns:
        success: bool
    """
    # 做点准备工作
    db = DbOperator()
    success, item = db.find_article(article_id)
    if not success:
        # log it
        print("can't find article by article_id: " + str(article_id))
        logger.error("can't find article by article_id: " + str(article_id))
        return False
    open_id = item['open_id']
    success, result = db.find_user(open_id)
    if not success:
        logger.error("article & user no match: "
                     + " ".join(map(str, [article_id, open_id])))
        return False
    email = result['email']
    # 开始更新数据库
    if not valid:  # 当文章已不可访问
        update_info = (article_id, optionals)
        _stop_watching(update_info, item, email, db)
    else:
        prev_status = item['status']
        if prev_status == STATUS_NORMAL_OB:  # 正常观察状态,无需额外操作
            return True
        elif prev_status == STATUS_NEW_UNKNOWN:  # 初次完成观察，制作备份
            if backup_path is None:
                print("backup addr not valid")
                return False
            else:
                new_path = _backup_article(article_id, backup_path)
                try:
                    title = optionals["title"]
                except KeyError:
                    title = ''
                if new_path is None:
                    return False
                article_info = (article_id, new_path, STATUS_NORMAL_OB, title) # 制作备份完成，状态更新
                return db.update_article(article_info)
        else:
            print("unknown status!")
            logger.error("article status Error: "
                         + " ".join(map(str, [article_id, open_id, prev_status])))

    return True

class Observer:
    def __init__(self):
        self.q = Checker_Queue(max_size=500)

    def init_checker(self):
        self.ac = Article_Checker(self.q, sleeping_time=6, saving_path='',
                                    call_back_func=update_article_status)
        self.ac.start()
        logger.info("Article_Checker init done")

    def ob_this_one(self, URL, open_id):
        """Add a new article to watch list.
        This function receive a unique(guaranteed by caller) article URL to be
        observed. It first check the accessibility of the article, then add
        it to watch list in database.

        Args:
            URL : str

        Returns:
            success: bool
        """
        db = DbOperator()
        # 先添加一次，然后获取 article_id 给 Checker 用
        success, article_id = db.db_add_helper(URL, open_id,
                                               FAKE_PATH_PLACE_HOLDER,
                                               STATUS_NEW_UNKNOWN)
        if not success:
            print("db_add_helper FAIL!!!")
            logger.warning("ob_this_one fail, add db fail: "
                           + " ".join(map(str, [article_id, URL, open_id])))
            return False
        self.q.put(article_id=article_id, url=URL,
                   download=True, block=True, timeout=1)
        # 放进 Checker 的队列就不管了。后续由回调函数实现
        return True

    def ob_all(self):
        """Observe all objects in watch list.
        No paras needed. It automatically fetches data from database,
        then loop through all items.

        Args:

        Returns:
            success: bool
        """
        db = DbOperator()
        success, watch_list = db.fetch_all_article()
        if not success:
            logger.warning("ob_all find nothing")
            return False
        for item in watch_list:
            article_id = item['article_id']
            URL = item['URL']
            status = item['status']
            start_date = item['start_date']

            if _out_of_date(start_date):
                # 超出30天的观察目标，停止观察
                backup_addr = item['backup_addr']
                update_article_status(article_id, False, 
                                      backup_addr, {"reason": REASON_OUT_OF_DATE})
                logger.info("move article to archive: "
                            + " ".join(map(str, [article_id, status, start_date])))
                continue
            else:
                if status == STATUS_NEW_UNKNOWN:  # 初次观察，需要制作备份
                    self.q.put(article_id=article_id, url=URL,
                               download=True, block=True, timeout=1)
                elif status == STATUS_NORMAL_OB:  # 正常观察期，不再制作备份
                    self.q.put(article_id=article_id, url=URL,
                               download=False, block=True, timeout=1)
                else:
                    logger.error("Unknow article status: "
                                 + " ".join(map(str, [article_id, URL, status])))
        return True

    def get_cur_q_size(self):
        """Current pending items in Checker's queue.
        Returns:
            qsize: int
        """
        return self.q.get_queue_size()


def send_user_check_email(email):
    """Send first check email to user.

    Args:
        email : str

    Returns:
        success: bool
    """
    receiver = [email]
    body_text = ("您好，我是折光观察者，服务于公众号「时间从来不回答」\n"
                "收到这封邮件是因为您在公众号内初次绑定了邮箱\n"
                "我向您发送邮件以确认您此后能正确收到邮件通知\n"
                "若本邮件被误分类为垃圾邮件，建议点击「这不是垃圾邮件」帮我逃离\n"
                "另外，建议将本邮箱添加至通讯录/联系人列表，避免此后通知邮件再被误分类\n"
                "\n\n\n如果您不知道为何收到本邮件，请联系：youdangls@gmail.com 处理")
    msg = {'Subject': '初次绑定邮箱通知', 'Body': body_text}
    return send_mail(receiver, contents=msg)

def _stop_watching(update_info, item, email, db):
    article_id, optionals = update_info
    URL = item['URL']
    if item['backup_addr'] == FAKE_PATH_PLACE_HOLDER:
        backup_addr = None
    else:
        backup_addr = item['backup_addr']
    try:
        reason = optionals["reason"]
    except KeyError:
        reason = REASON_NULL
    notify_user(email, URL, backup_addr, reason)
    try:
        item['status'] = reason2status[reason]
    except KeyError:
        logger.exception("_stop_watching reason2status ERROR")
    db.archive_article(item)
    logger.info("article expired, move to archive: "
                + " ".join(map(str, [article_id, URL])))
    return

def _backup_article(article_id, article_path):
    path = _get_path()
    if not os.path.exists(path):
        os.makedirs(path)
    file_path = os.path.join(path, str(article_id) + '.docx')
    if _save_file(file_path, article_path):
        return file_path
    else:
        return None


def _get_path():
    # 从当前日期生成存档路径
    now = datetime.datetime.now()
    date = now.strftime('%Y%m')
    return os.path.join(DEFAULT_PATH, date)


def _save_file(new_path, old_path):
    # 由调用者保证目标路径存在
    # 只是个mv动作
    try:
        shutil.move(old_path, new_path)
    except shutil.Error:
        print("save file fail!:", new_path, old_path)
        logger.exception("save file fail: "
                        + " ".join([new_path, old_path]))
        return False
    except IOError:
        logger.exception("sys IOError: " + " ".join([new_path, old_path]))
        return False
    print("save file done:", new_path)
    return True


def _out_of_date(date):
    """Is record out of date?
    We only observe each record for 30 days. After that,
    record will expire.

    Args:
        date : <class 'datetime.date'>

    Returns:
        answer : bool
    """
    today = datetime.date.today()
    interval = today - date # 两日期差距
    days = interval.days # 具体的天数(int)
    return (days > MAX_OB_DAYS)
