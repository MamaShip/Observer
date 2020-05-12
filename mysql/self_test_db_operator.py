
from db_operator import DbOperator

if __name__ == "__main__":
    print("start running")
    
    worker = DbOperator()

    '''
    print(">>>> test add_user")
    result = worker.add_user(("test_user2","test2@email.com"))
    '''

    '''
    print(">>>> test find_user")
    print("find exist user:")
    success, result = worker.find_user("test_user2")
    print(success)
    print(result)
    print("find invalid user:")
    success, result = worker.find_user("nobody")
    print(success)
    print(result)
    '''
    '''
    print(">>>> test update_user")
    success = worker.update_user(("test_user1", "newmail1111@email.com"))
    print(success)
    print("update invalid user:")
    success = worker.update_user(("nobody", "newmail@xxxil.com"))
    print(success)
    '''
    '''
    print(">>>> test remove_user")
    success = worker.remove_user("test_user1")
    print(success)
    print("remove invalid user:")
    success = worker.remove_user("nobody")
    print(success)
    '''
    
    print(">>>> test add_article")
    result = worker.add_article(("www.google.com","test2","/path/to/store/file"))
    print(result)
    
    '''
    print(">>>> test update_article") # (article_id, backup_addr, status)
    success = worker.update_article((1, "new/path/to/store", 2))
    print(success)
    print("update invalid article:")
    success = worker.update_article((100, "new/path/to/store", 3))
    print(success)
    '''

    print(">>>> test remove_article")
    success = worker.remove_article(1)
    print(success)
    print("remove invalid article:")
    success = worker.remove_article(100)
    print(success)

    print("finish")

# This dosn't work at all
'''
import pytest
class TestDbOperator:
    def test_get_record(self):
        result = x.get_record(111)
        assert True == result
'''


