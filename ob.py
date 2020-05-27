import os
import logging
from datetime import datetime
import shutil
from article_checker.article_checker import Checker_Queue, Article_Checker
from database.db_operator import DbOperator
from mail.mail import send_mail
'''
LOG_FORMAT = "%(asctime)s - %(levelname)s - %(message)s"
logging.basicConfig(filename='observer.log',
                    level=logging.DEBUG, format=LOG_FORMAT)
'''
# 获取 Logger 对象(跟main共用一个logger)
logger = logging.getLogger("main")

DEFAULT_PATH = "/var/wx/article"
FAKE_PATH_PLACE_HOLDER = "placeholder"


def notify_user(email, URL, backup_addr):
    """Send email to users.
    Send email to users for notifying article expiration.
    With backup file attached.

    Args:
        URL : str
        backup_addr : str

    Returns:
        success: bool
    """
    receiver = [email]

    if backup_addr == None:
        addition = '\n截止观察结束时，没有成功备份的文档存留。\n如有疑问请联系管理员：youdangls@gmail.com'
        attach = []
    else:
        addition = '\n附件是文章备份，请查收。'
        attach = [backup_addr]
    body_text = '您观察的文章：' + URL + ' 已失效。' + addition
    msg = {'Subject': '您的观察目标有状态更新', 'Body': body_text}
    return send_mail(receiver, contents=msg, attachments=attach)


def update_article_status(article_id, valid, backup_path=None):
    # 做点准备工作
    db = DbOperator()
    success, item = db.find_article(article_id)
    if not success:
        # log it
        print("can't find article by article_id: " + str(article_id))
        logger.error("can't find article by article_id: " + str(article_id))
        return False
    URL = item['URL']
    open_id = item['open_id']
    success, result = db.find_user(open_id)
    if not success:
        logger.error("article & user no match: "
                      + " ".join(map(str, [article_id, open_id])))
        return False
    email = result['email']

    if not valid:  # 当文章已不可访问
        if item['backup_addr'] == FAKE_PATH_PLACE_HOLDER:
            backup_addr = None
        else:
            backup_addr = item['backup_addr']
        notify_user(email, URL, backup_addr)
        db.archive_article(item)
        logger.info("article expired, move to archive: "
                     + " ".join(map(str, [article_id, URL])))
    else:
        prev_status = item['status']
        if prev_status == 1:  # 正常观察状态,无需额外操作
            return True
        elif prev_status == 0:  # 初次完成观察，制作备份
            if backup_path == None:
                print("backup addr not valid")
                return False
            else:
                new_path = _backup_article(article_id, backup_path)
                if new_path == None:
                    return False
                return _db_update(db, article_id, new_path, 1)  # 制作备份完成，状态更新成1
        else:
            print("unknown status!")
            logger.error("article status Error: "
                          + " ".join(map(str, [article_id, open_id, prev_status])))

    return True


class Observer:
    def __init__(self):
        self.q = Checker_Queue(max_size=500)

    def init_checker(self):
        self.ac = Article_Checker(
            self.q, sleeping_time=6, saving_path='', call_back_func=update_article_status)
        self.ac.start()

    def __del__(self):
        self.ac.join()

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
        success, article_id = _db_add(db, URL, open_id, FAKE_PATH_PLACE_HOLDER)
        if not success:
            print("_db_add FAIL!!!")
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
        # tmp code
        print("ob_all running")
        # tmp code end
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
                update_article_status(article_id, False, backup_addr)
                pass
            else:
                if status == 0:  # 初次观察，需要制作备份
                    self.q.put(article_id=article_id, url=URL,
                               download=True, block=True, timeout=1)
                elif status == 1:  # 正常观察期，不再制作备份
                    self.q.put(article_id=article_id, url=URL,
                               download=False, block=True, timeout=1)
                else:
                    logger.error("Unknow article status: "
                                  + " ".join(map(str, [article_id, URL, status])))
        return True


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
    now = datetime.now()
    date = now.strftime('%Y%m')
    return os.path.join(DEFAULT_PATH, date)


def _save_file(new_path, old_path):
    # 由调用者保证目标路径存在
    # 只是个mv动作
    try:
        shutil.move(old_path, new_path)
    except:
        print("save file fail!:", new_path, old_path)
        logger.error("save file fail: "
                            + " ".join([new_path,old_path]))
        return False
    print("save file done:", new_path)
    return True


def _out_of_date(date):
    # TODO
    return False


def _db_add(db, URL, open_id, backup_addr):
    """Private function for first time add info to database.

    Args:
        URL : str
        open_id : str
            user id offered by wechat.
        backup_addr: str
            the path of archive file.

    Returns:
        success: bool
    """
    article = (URL, open_id, backup_addr)
    if not db.add_article(article):  # 执行add操作
        logger.warning("_db_add fail, with paras:"
                        + " ".join(map(str, article)))
        return False, None
    _, result = db.find_my_article(open_id)  # add完读出来获取 article_id
    for item in result:
        if item['URL'] == URL:
            return True, item['article_id']
    logger.warning("_db_add success, but can't read. with paras:"
                    + " ".join(map(str, article)))
    return False, None


def _db_update(db, article_id, backup_addr, status):
    """Private function for update status to database.

    Args:
        article_id : int
        backup_addr : str
            the path of archive file.
        status: int
            watch status.(temporarily not used)

    Returns:
        success: bool
    """
    article = (article_id, backup_addr, status)
    return db.update_article(article)


def _db_archive(db, item):
    """Move watch item to archive database.
    Because this article is no longer valid. We don't
    have to watch it later. But archive for total report
    or sth. 

    Args:
        item : dict
            {'article_id', 'URL', 'open_id', 'backup_addr',
            'start_date','status'}

    Returns:
        success: bool
    """
    return db.archive_article(item)
