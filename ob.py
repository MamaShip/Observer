import os
import logging
from datetime import datetime
from article_checker import Checker
from database.db_operator import DbOperator
from mail.mail import send_mail

LOG_FORMAT = "%(asctime)s - %(levelname)s - %(message)s"
logging.basicConfig(filename='observer.log',
                    level=logging.DEBUG, format=LOG_FORMAT)
DEFAULT_PATH = "/var/wx/article"
FAKE_PATH_PLACE_HOLDER = "placeholder"

class Observer:
    def __init__(self):
        self.ckr = Checker()
        self.db  = DbOperator()

    def backup_article(self, URL, article_id):
        # TODO: 这部分要确认过 Article_Checker 的功能之后再确认怎么写
        try:
            web = self.ckr.get_content(URL)
        except:
            print("get_content fail")
            # log it
            return None
        if web:
            path = self.get_path()
            file_path = os.path.join(path, str(article_id) + '.pdf')
            self.save_file(file_path, web)
            return file_path
        else:
            return None

    def get_path(self):
        # 从当前日期生成存档路径
        now = datetime.now()
        date = now.strftime('%Y%m')
        return os.path.join(DEFAULT_PATH, date)

    def save_file(self, path, web):
        # TODO: 取决于 Article_Checker 是怎么写的
        # 这里可能只是个mv动作
        pass
            
    def _db_add(self, URL, open_id, backup_addr):
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
        if not self.db.add_article(article): # 执行add操作
            logging.warning("_db_add fail, with paras:" 
                            + " ".join(map(str, article)))
            return False, None
        _, result = self.db.find_my_article(open_id) # add完读出来获取 article_id
        for item in result:
            if item['URL'] == URL:
                return True, item['article_id']
        logging.warning("_db_add success, but can't read. with paras:"
                        + " ".join(map(str, article)))
        return False, None

    def _db_update(self, article_id, backup_addr, status):
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
        return self.db.update_article(article)

    def _db_archive(self, item):
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
        return self.db.archive_article(item)

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
        if self.ckr.check_validation(URL):
            success, article_id = self._db_add(URL, open_id, FAKE_PATH_PLACE_HOLDER)
            if not success:
                return False
            # 先添加一次，然后获取 article_id 进行备份，再更新一次
            path = self.backup_article(URL, article_id)
            if path:
                self._db_update(article_id, path, 0)
            else:
                # log it
                logging.warning("article valid, but backup fail: " + URL)
                print("can't backup")
        else:
            return False # 初次检查就不可访问时，由主逻辑处理这个错误
                         # 向发送信息的用户回复内容有误，观察结束。
        return True

    def ob_all(self):
        """Observe all objects in watch list.
        No paras needed. It automatically fetches data from database,
        then loop through all items.

        Args:
        
        Returns:
            success: bool
        """
        success, watch_list = self.db.fetch_all_article()
        if not success:
            logging.warning("ob_all find nothing")
            return False
        for item in watch_list:
            article_id  = item['article_id']
            URL         = item['URL']
            open_id     = item['open_id']
            backup_addr = item['backup_addr']
            status      = item['status']

            if self.ckr.check_validation(URL):
                # check backup
                if FAKE_PATH_PLACE_HOLDER == backup_addr: # need try backup again
                    path = self.backup_article(URL, article_id)
                    if path:
                        self._db_update(article_id, path, status)
                    else:
                        logging.warning("article valid, but backup fail: " + URL)
                        print("can't backup")
            else:
                self.notify_user(article_id, URL, open_id, backup_addr)
                self._db_archive(item)
                logging.info("article expired, move to archive: " 
                            + " ".join(map(str, [article_id, URL])))
        return True
    
    def notify_user(self, article_id, URL, open_id, backup_addr):
        """Send email to users.
        Send email to users for notifying article expiration.
        With backup file attached.

        Args:
            article_id : int
            URL : str
            open_id : str
            backup_addr : str
        
        Returns:
            success: bool
        """
        success, result = self.db.find_user(open_id)
        if not success:
            logging.error("article & user no match: " 
                        + " ".join(map(str, [article_id, open_id])))
            return False
        email    = result['email']
        receiver = [email]
        msg      = {'Subject': '您的观察目标有状态更新',
                    'Body': '您观察的文章：' + URL
                            + '已失效。\n附件是文章备份，请查收。'}
        attach   = [backup_addr]
        return send_mail(receiver, contents=msg, attachments=attach)


