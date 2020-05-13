
from db_operator import DbOperator

worker = DbOperator()

def self_test_add_user():
    print(">>>> test add_user")
    open_id = input("input user open_id:")
    email = input("input user email:")
    result = worker.add_user((open_id,email))
    print(result)


def self_test_find_user():
    
    print(">>>> test find_user")
    print("find exist user:")
    open_id = input("input user open_id:")
    success, result = worker.find_user(open_id)
    print(success)
    print(result)
    print("find invalid user:")
    success, result = worker.find_user("nobody")
    print(success)
    print(result)
    

def self_test_update_user():
    
    print(">>>> test update_user")
    open_id = input("input user open_id:")
    email = input("input user's new email:")
    success = worker.update_user((open_id, email))
    print(success)
    print("update invalid user:")
    success = worker.update_user(("nobody", "newmail@xxxil.com"))
    print(success)
    


def self_test_remove_user():
    
    print(">>>> test remove_user")
    open_id = input("input user open_id:")
    success = worker.remove_user(open_id)
    print(success)
    print("remove invalid user:")
    success = worker.remove_user("nobody")
    print(success)
    

def self_test_add_article():
    print(">>>> test add_article")
    URL = input("input URL:")
    open_id = input("input user open_id:")
    bak_addr = input("input path:")
    result = worker.add_article((URL,open_id,bak_addr))
    print(result)

def self_test_fetch_all_article():
    print(">>>> test fetch_all_article")
    success, result = worker.fetch_all_article()
    print(success)
    print(result)

def self_test_update_article():
    print(">>>> test update_article") # (article_id, backup_addr, status)
    article_id = input("input article_id:")
    backup_addr = input("input backup_addr:")
    status = input("input status:")
    success = worker.update_article((article_id, backup_addr, status))
    print(success)
    print("update invalid article:")
    success = worker.update_article((10000, "new/path/to/store", 3))
    print(success)
    

def self_test_remove_article():
    print(">>>> test remove_article")
    article_id = input("input article_id:")
    success = worker.remove_article(article_id)
    print(success)
    print("remove invalid article:")
    success = worker.remove_article(100)
    print(success)



if __name__ == "__main__":
    print("start running")
    self_test_add_user()
    self_test_add_user()
    self_test_add_user()
    self_test_find_user()
    self_test_update_user()
    self_test_remove_user()
    self_test_add_article()
    self_test_fetch_all_article()
    self_test_update_article()
    self_test_fetch_all_article()
    self_test_remove_article()
    self_test_fetch_all_article()

    print("finish")

# This dosn't work at all
'''
import pytest
class TestDbOperator:
    def test_get_record(self):
        result = x.get_record(111)
        assert True == result
'''


