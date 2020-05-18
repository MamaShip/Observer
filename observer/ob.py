from datetime import datetime
from article_checker import Checker
from ..mysql.db_operator import DbOperator
from ..mail.db_operator import send_mail

DEFAULT_PATH = "/var/wx/article/"

class Observer:
    def __init__(self):
        self.ckr = Checker()
        self.db  = DbOperator()

    def backup_article(self, URL):
        # TODO: 这部分要确认过 Article_Checker 的功能之后再确认怎么写
        try:
            web = self.ckr.get_content(URL)
        except:
            print("get_content fail")
            # log it
            return None
        if web:
            path = self.get_path()
            self.save_file(path, web)
            return path
        else:
            return None

    def get_path(self):
        # 从当前日期生成存档路径
        # TODO：如何生成比较好
        now = datetime.now()
        date = now.strftime('%Y%m%d')
        return DEFAULT_PATH + date

    def save_file(self, path, web):
        # TODO: 要根据路径下已有的文件来决定下一个文件命名
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
        return self.db.add_article(article)

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
            # do sth
            path = self.backup_article(URL)
            if path:
                self._db_add(URL, open_id, path)
            else:
                # log it
                self._db_add(URL, open_id, None)
        else:
            return False # 初次检查就不可访问时，由主逻辑处理这个错误
                         # 向发送信息的用户回复内容有误，观察结束。
        return True

    def ob_all(self):
        """Observe all objects in watch list.
        No paras needed. It automatically fetch data from database,
        then do ob for all items.

        Args:
        
        Returns:
            success: bool
        """
        success, watch_list = self.db.fetch_all_article()
        if not success:
            # log it?
            return False
        for item in watch_list:
            article_id  = item['article_id']
            URL         = item['URL']
            open_id     = item['open_id']
            backup_addr = item['backup_addr']
            status      = item['status']

            if self.ckr.check_validation(URL):
                # do sth
                if not path: # TODO: 这个条件需要再确认
                    path = self.backup_article(URL)
                    if path:
                        self._db_update(article_id, backup_addr, status)
                    else:
                        # log it
                        pass
            else:
                self.notify_user(article_id, URL, open_id, backup_addr)
                self._db_archive(item)
        return True
    
    def notify_user(self, article_id, URL, open_id, backup_addr):
        success, result = self.db.find_user(open_id)
        if not success:
            return False
        email    = result['email']
        receiver = [email]
        msg      = {'Subject': '您的观察目标有状态更新',
                    'Body': '您观察的文章：' + URL
                            + '已失效。\n附件是文章备份，请查收。'}
        attach   = [backup_addr]
        return send_mail(receiver, contents=msg, attachments=attach)


