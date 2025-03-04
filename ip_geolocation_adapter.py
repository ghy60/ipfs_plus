#!/usr/bin/env python3
# -*- coding: utf-8 -*-

"""
MySQL连接器适配器

该脚本提供了一个统一的接口来使用不同的MySQL连接器。
可以自动检测系统上可用的MySQL连接器库，并使用最合适的一个。
"""

import sys
import importlib.util

class MySQLAdapter:
    """MySQL连接器适配器类"""
    
    def __init__(self):
        self.connector_type = self._detect_connector()
        if not self.connector_type:
            print("错误: 未找到可用的MySQL连接器")
            print("请安装以下连接器之一: mysql-connector-python, mysqlclient, pymysql")
            sys.exit(1)
            
        print(f"使用 {self.connector_type} 连接MySQL")
    
    def _detect_connector(self):
        """检测系统上可用的MySQL连接器"""
        connectors = [
            {"name": "mysql.connector", "module": "mysql.connector"},
            {"name": "MySQLdb", "module": "MySQLdb"},
            {"name": "pymysql", "module": "pymysql"}
        ]
        
        for connector in connectors:
            if importlib.util.find_spec(connector["module"]):
                return connector["name"]
        
        return None
    
    def connect(self, config):
        """连接到MySQL数据库"""
        if self.connector_type == "mysql.connector":
            import mysql.connector
            return mysql.connector.connect(**config)
        
        elif self.connector_type == "MySQLdb":
            import MySQLdb
            # MySQLdb使用不同的参数命名
            adjusted_config = {
                "host": config.get("host"),
                "user": config.get("user"),
                "passwd": config.get("password"),
                "db": config.get("database")
            }
            return MySQLdb.connect(**adjusted_config)
        
        elif self.connector_type == "pymysql":
            import pymysql
            return pymysql.connect(
                host=config.get("host"),
                user=config.get("user"),
                password=config.get("password"),
                database=config.get("database")
            )
            
    def execute_query(self, conn, query, params=None):
        """执行SQL查询"""
        cursor = conn.cursor()
        if params:
            cursor.execute(query, params)
        else:
            cursor.execute(query)
        return cursor
    
    def commit(self, conn):
        """提交事务"""
        conn.commit()
        
    def close(self, conn):
        """关闭连接"""
        conn.close()
        
    def fetchall(self, cursor):
        """获取所有结果"""
        return cursor.fetchall()
    
    def rowcount(self, cursor):
        """获取影响的行数"""
        return cursor.rowcount
        
# 使用示例
if __name__ == "__main__":
    db = MySQLAdapter()
    
    # 创建数据库配置
    db_config = {
        'host': 'localhost',
        'user': 'root',
        'password': 'password',
        'database': 'test'
    }
    
    try:
        # 连接数据库
        conn = db.connect(db_config)
        
        # 执行查询
        cursor = db.execute_query(conn, "SELECT VERSION()")
        
        # 获取结果
        result = db.fetchall(cursor)
        print(f"MySQL版本: {result[0][0]}")
        
        # 关闭连接
        db.close(conn)
        
    except Exception as e:
        print(f"数据库连接测试失败: {e}")
        sys.exit(1)
    
    print("数据库连接测试成功!")
    sys.exit(0) 