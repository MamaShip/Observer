# -*-coding:utf-8 -*-
'''DataBase Operator
Work on MySQL. Offerring a class called DbOperator, which
handle the connection of MySQL and provide several basic
operations. 
'''
import logging
import mysql.connector
from mysql.connector import errorcode

# !!! 已知问题：当数据库内没有目标项目时，对其做 update/delete 都不会报错

LOG_FORMAT = "%(asctime)s - %(levelname)s - %(message)s"
logging.basicConfig(filename='mysql.log',
                    level=logging.DEBUG, format=LOG_FORMAT)

DB_NAME = 'ObDb'
TABLES = {}
TABLES['users'] = (
    "CREATE TABLE `users` ("
    "  `user_id` INT UNSIGNED NOT NULL AUTO_INCREMENT,"
    "  `open_id` varchar(128) NOT NULL,"
    "  `email` varchar(255) NOT NULL,"
    "  `reg_date` date NOT NULL,"
    "  PRIMARY KEY (`user_id`), UNIQUE KEY `open_id` (`open_id`)"
    ") ENGINE=InnoDB")

TABLES['articles'] = (
    "CREATE TABLE `articles` ("
    "  `article_id` INT UNSIGNED NOT NULL AUTO_INCREMENT,"
    "  `URL` varchar(2048) NOT NULL,"
    "  `open_id` varchar(128) NOT NULL,"
    "  `backup_addr` varchar(100) NOT NULL,"
    "  `start_date` date NOT NULL,"
    "  `status` INT UNSIGNED NOT NULL,"
    "  PRIMARY KEY (`article_id`)"
    ") ENGINE=InnoDB")

TABLES['archive'] = (
    "CREATE TABLE `archive` ("
    "  `article_id` INT UNSIGNED NOT NULL AUTO_INCREMENT,"
    "  `URL` varchar(2048) NOT NULL,"
    "  `open_id` varchar(128) NOT NULL,"
    "  `backup_addr` varchar(100) NOT NULL,"
    "  `start_date` date NOT NULL,"
    "  `status` INT UNSIGNED NOT NULL,"
    "  PRIMARY KEY (`article_id`)"
    ") ENGINE=InnoDB")


class DbOperator:
    def __init__(self):
        # 打开数据库连接
        self.db = mysql.connector.connect(
            host="localhost",            # 数据库主机地址
            user="DbOperator",           # 数据库用户名
            passwd="DoNotAnswer2048!",   # 数据库密码
            database=DB_NAME)            # 直接选择特定数据库
        print("db connection created")

    def __del__(self):
        # 关闭数据库连接
        self.db.close()
        print("db connection deleted")

    def _commit_cmd(self, cmd, parameters):
        """Commit a cmd.
        When making any changes to database, you should
        commit it to make it effective. This operation may 
        cause error. So you have to handle it and rollback.

        Args:
            cmd: str
            parameters: tuple of variables in your cmd

        Returns:
            success: True/False - if any error happened,
                    this will be False. 
        """
        success = True
        cursor = self.db.cursor()
        try:
            cursor.execute(cmd, parameters)
            # Commit the changes
            self.db.commit()
        except:
            self.db.rollback()
            success = False
            # log it
            logging.warning("commit fail when executing cmd: " + cmd)
            logging.warning("> with parameters: "
                            + " ".join(map(str, parameters)))
        cursor.close()
        return success

    def _execute_cmd(self, cmd, parameters):
        """Execute a query cmd.
        Query cmd doesn't make any change to database.
        So you don't have to commit it and/or rollback.
        We only care about results.

        Args:
            cmd: str
            parameters: tuple of variables in your cmd

        Returns:
            result: list. can be empty
        """
        cursor = self.db.cursor()
        result = []
        cursor.execute(cmd, parameters)
        for item in cursor:
            result.append(item)
        cursor.close()
        return result

    def add_user(self, user):
        """Register new user.
        Add user info to database.

        Args:
            user: A tuple of user info like : (open_id, email)

        Returns:
            success or not: True/False
        """
        insert_new_user = (
            "INSERT INTO users (open_id, email, reg_date) "
            "VALUES (%s, %s, NOW());")
        return self._commit_cmd(insert_new_user, user)

    def find_user(self, open_id):
        """Get user info.
        Get user info from database. If user dosn't exist,
        return False and empty dict.

        Args:
            open_id: str offered by wechat

        Returns:
            success: True/False
            result: a dict of {'user_id','open_id','email','reg_date'}
        """
        success = True
        query = ("SELECT user_id, email, reg_date FROM users "
                 "WHERE open_id = %s;")
        query_result = self._execute_cmd(query, (open_id,))
        result = {}
        for (user_id, email, reg_date) in query_result:
            result['user_id'] = user_id
            result['open_id'] = open_id
            result['email'] = email
            result['reg_date'] = reg_date
        if len(result) == 0:
            success = False
            # log it
            logging.info("fetch no item with cmd:" + query)
            logging.info("> open_id= " + str(open_id))
        return success, result

    def update_user(self, user):
        """Update user info.
        Change user info to database.

        Args:
            user: A tuple of user info like : (open_id, email)

        Returns:
            success: True/False
        """
        open_id, email = user
        update = ("UPDATE users SET email=%s WHERE open_id=%s;")
        parameters = (email, open_id)
        return self._commit_cmd(update, parameters)

    def remove_user(self, open_id):
        """remove user info.
        Delete user info from database.

        Args:
            open_id: str offered by wechat

        Returns:
            success: True/False
        """
        delete = ("DELETE FROM users WHERE open_id=%s;")
        return self._commit_cmd(delete, (open_id,))

    def add_article(self, article):
        """Register new article to be observed.
        Add article info to database.
        Only registered users can add articles. This should be guaranteed 
        by caller.

        Args:
            user: A tuple of article info like : (URL, open_id, backup_addr)

        Returns:
            success or not: True/False
        """
        insert_new_article = (
            "INSERT INTO articles (URL, open_id, backup_addr, start_date, status) "
            "VALUES (%s, %s, %s, NOW(), 1);")  # status 目前没有使用，强制赋1
        return self._commit_cmd(insert_new_article, article)

    def find_my_article(self, open_id):  # TODO
        """Get article list of one user.
        Get article info from database. If article dosn't exist,
        return False and empty list.

        Args:
            open_id: str offered by wechat

        Returns:
            success: True/False
            result: a list of dict like {'URL','open_id',
                    'backup_addr','start_date','status'}
        """
        success = True
        query = ("SELECT URL, backup_addr, start_date, status FROM articles "
                 "WHERE open_id = %s;")
        query_result = self._execute_cmd(query, (open_id,))
        result = []
        for (URL, backup_addr, start_date, status) in query_result:
            item = {}
            item['URL'] = URL
            item['open_id'] = open_id
            item['backup_addr'] = backup_addr
            item['start_date'] = start_date
            item['status'] = status
            result.append(item)
        if len(result) == 0:
            success = False
            # log it
            logging.info("fetch no item with cmd: " + query)
            logging.info("> open_id= " + str(open_id))
        return success, result

    def update_article(self, article):
        """Update article info.
        Change(Set) article info to database.

        Args:
            article: A tuple of article info like: (article_id, backup_addr, status)

        Returns:
            success: True/False
        """
        article_id, backup_addr, status = article
        update = (
            "UPDATE articles SET backup_addr=%s, status=%s WHERE article_id=%s;")
        parameters = (backup_addr, status, article_id)
        return self._commit_cmd(update, parameters)

    def remove_article(self, article_id):
        """remove article record.
        Delete article record from database.

        Args:
            article_id: int - primary key of article records

        Returns:
            success: True/False
        """
        delete = ("DELETE FROM articles WHERE article_id=%s;")
        return self._commit_cmd(delete, (article_id,))

    def fetch_all_article(self):
        """Get the whole article list.
        Fetch all article info from database.

        Args:
            Nothing

        Returns:
            success: True/False
            result: a list of dict like {'article_id', 'URL',
                    'open_id','backup_addr','start_date','status'}
        """
        success = True
        query = ("SELECT article_id, URL, open_id, backup_addr, start_date, status FROM articles;")
        query_result = self._execute_cmd(query, None)
        result = []
        for (article_id, URL, open_id, backup_addr, start_date, status) in query_result:
            item = {}
            item['article_id'] = article_id
            item['URL'] = URL
            item['open_id'] = open_id
            item['backup_addr'] = backup_addr
            item['start_date'] = start_date
            item['status'] = status
            result.append(item)
        if len(result) == 0:
            success = False
            # log it
            logging.info("fetch no item with cmd: " + query)
        return success, result

    def archive_article(self, article):
        article_id = article['article_id']
        self.remove_article(article_id)
        new_record = (article_id, article['URL'], article['open_id'],
                    article['backup_addr'], article['start_date'])
        return self._add_archive(new_record)

    def _add_archive(self, article):
        """Move record to archive table.
        Archive info for later report.

        Args:
            user: A tuple of article info
            (article_id, URL, open_id, backup_addr, start_date)

        Returns:
            success or not: True/False
        """
        insert_new_article = (
            "INSERT INTO archive (article_id, URL, open_id, backup_addr, start_date, status) "
            "VALUES (%s, %s, %s, %s, %s, 8);")  # status 8 用来表示存档的文件
        return self._commit_cmd(insert_new_article, article)

    def is_table_exist(self, table_name):  # 代码里不打算做这个检测了，由用户保证
        return True


class DbCreator:
    def __init__(self):
        # 打开数据库连接
        self.db = mysql.connector.connect(
            host="localhost",            # 数据库主机地址
            user="DbOperator",           # 数据库用户名
            passwd="DoNotAnswer2048!",   # 数据库密码
            database=DB_NAME)            # 直接选择特定数据库
        print("db connection created")

    def __del__(self):
        # 关闭数据库连接
        self.db.close()
        print("db connection deleted")

    def create_table(self):
        """Create table.
        Create 2 tables defined by global variables: TABLES
        """
        cursor = self.db.cursor()        # 获取操作游标
        logging.info("try create_table()")
        for table_name in TABLES:
            table_description = TABLES[table_name]
            try:
                print("Creating table {}: ".format(table_name), end='')
                cursor.execute(table_description)
            except mysql.connector.Error as err:
                if err.errno == errorcode.ER_TABLE_EXISTS_ERROR:
                    print("already exists.")
                    logging.info("table already exists.")
                else:
                    print(err.msg)
                    logging.warning("create_table() ERROR.")
                    logging.warning(err.msg)
            else:
                print("OK")
                logging.info("create_table() OK")
        cursor.close()


if __name__ == "__main__":
    # 创建数据表是一次性操作，此后不再需要执行
    worker = DbCreator()
    worker.create_table()
    print("create tables finish")

