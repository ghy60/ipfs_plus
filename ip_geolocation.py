#!/usr/bin/env python3
# -*- coding: utf-8 -*-

"""
IP地理位置解析服务

该脚本用于解析MySQL数据库中存储的IP地址的地理位置信息，
并将解析结果（国家、城市、经纬度）存储回数据库。
只解析IPv4地址，忽略IPv6地址。
"""

import time
import logging
import ipaddress
import mysql.connector
import requests
from datetime import datetime
import sys

# 配置日志记录
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(name)s - %(levelname)s - %(message)s',
    handlers=[
        logging.FileHandler("ip_geolocation.log"),
        logging.StreamHandler(sys.stdout)
    ]
)
logger = logging.getLogger("IPGeolocation")

# 数据库配置
DB_CONFIG = {
    'host': '42.194.236.54',
    'user': 'root',
    'password': 'As984315#',
    'database': 'ipfs_nodes'
}

# IP地理位置解析API配置
IP_API_URL = "http://ip-api.com/json/{}"
BATCH_SIZE = 100  # 每次处理的IP数量
SLEEP_TIME = 60   # 处理完一批IP后的等待时间（秒）
API_RATE_LIMIT = 45  # IP-API免费版每分钟限制查询次数
QUERY_INTERVAL = 60 / API_RATE_LIMIT  # 每次查询的间隔时间（秒）

def is_valid_ipv4(ip_str):
    """
    检查是否为有效的IPv4地址
    """
    try:
        ip = ipaddress.ip_address(ip_str)
        return ip.version == 4 and not ip.is_private and not ip.is_loopback and not ip.is_link_local
    except ValueError:
        return False

def ensure_geo_columns_exist():
    """
    确保数据库表中存在地理位置信息的列
    """
    try:
        conn = mysql.connector.connect(**DB_CONFIG)
        cursor = conn.cursor()
        
        # 检查是否存在这些列
        cursor.execute("""
            SELECT COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS
            WHERE TABLE_SCHEMA = %s AND TABLE_NAME = 'node_addresses'
            AND COLUMN_NAME IN ('country', 'city', 'latitude', 'longitude', 'region', 'timezone', 'isp', 'geo_updated_at')
        """, (DB_CONFIG['database'],))
        
        existing_columns = [col[0] for col in cursor.fetchall()]
        
        # 需要添加的列
        columns_to_add = []
        if 'country' not in existing_columns:
            columns_to_add.append("ADD COLUMN `country` VARCHAR(100) NULL")
        if 'city' not in existing_columns:
            columns_to_add.append("ADD COLUMN `city` VARCHAR(100) NULL")
        if 'latitude' not in existing_columns:
            columns_to_add.append("ADD COLUMN `latitude` DECIMAL(10, 6) NULL")
        if 'longitude' not in existing_columns:
            columns_to_add.append("ADD COLUMN `longitude` DECIMAL(10, 6) NULL")
        if 'region' not in existing_columns:
            columns_to_add.append("ADD COLUMN `region` VARCHAR(100) NULL")
        if 'timezone' not in existing_columns:
            columns_to_add.append("ADD COLUMN `timezone` VARCHAR(50) NULL")
        if 'isp' not in existing_columns:
            columns_to_add.append("ADD COLUMN `isp` VARCHAR(255) NULL")
        if 'geo_updated_at' not in existing_columns:
            columns_to_add.append("ADD COLUMN `geo_updated_at` TIMESTAMP NULL")
        
        # 如果有列需要添加，则执行ALTER TABLE命令
        if columns_to_add:
            alter_query = "ALTER TABLE node_addresses " + ", ".join(columns_to_add)
            cursor.execute(alter_query)
            conn.commit()
            logger.info(f"Added {len(columns_to_add)} new columns to node_addresses table")
        
        cursor.close()
        conn.close()
        return True
    except Exception as e:
        logger.error(f"Error ensuring geo columns exist: {e}")
        return False

def get_unprocessed_ips(limit=BATCH_SIZE):
    """
    获取未处理的IPv4地址
    """
    try:
        conn = mysql.connector.connect(**DB_CONFIG)
        cursor = conn.cursor()
        
        cursor.execute("""
            SELECT id, host FROM node_addresses
            WHERE geo_updated_at IS NULL
            LIMIT %s
        """, (limit,))
        
        result = cursor.fetchall()
        cursor.close()
        conn.close()
        
        # 过滤出有效的IPv4地址
        valid_ips = []
        for row_id, ip in result:
            if is_valid_ipv4(ip):
                valid_ips.append((row_id, ip))
        
        return valid_ips
    except Exception as e:
        logger.error(f"Error getting unprocessed IPs: {e}")
        return []

def get_ip_geolocation(ip):
    """
    使用ip-api.com获取IP地址的地理位置信息
    """
    try:
        response = requests.get(IP_API_URL.format(ip), timeout=5)
        data = response.json()
        
        if data.get('status') == 'success':
            return {
                'country': data.get('country'),
                'city': data.get('city'),
                'latitude': data.get('lat'),
                'longitude': data.get('lon'),
                'region': data.get('regionName'),
                'timezone': data.get('timezone'),
                'isp': data.get('isp')
            }
        else:
            logger.warning(f"Failed to get geolocation for IP {ip}: {data.get('message', 'Unknown error')}")
            return None
    except Exception as e:
        logger.error(f"Error getting geolocation for IP {ip}: {e}")
        return None

def update_ip_geolocation(row_id, geo_data):
    """
    更新IP地址的地理位置信息到数据库
    """
    try:
        conn = mysql.connector.connect(**DB_CONFIG)
        cursor = conn.cursor()
        
        update_query = """
            UPDATE node_addresses
            SET country = %s, city = %s, latitude = %s, longitude = %s, 
                region = %s, timezone = %s, isp = %s, geo_updated_at = %s
            WHERE id = %s
        """
        
        cursor.execute(update_query, (
            geo_data.get('country'),
            geo_data.get('city'),
            geo_data.get('latitude'),
            geo_data.get('longitude'),
            geo_data.get('region'),
            geo_data.get('timezone'),
            geo_data.get('isp'),
            datetime.now(),
            row_id
        ))
        
        conn.commit()
        cursor.close()
        conn.close()
        return True
    except Exception as e:
        logger.error(f"Error updating geolocation for row {row_id}: {e}")
        return False

def mark_invalid_ip(row_id):
    """
    标记无效的IP地址为已处理
    """
    try:
        conn = mysql.connector.connect(**DB_CONFIG)
        cursor = conn.cursor()
        
        update_query = """
            UPDATE node_addresses
            SET geo_updated_at = %s
            WHERE id = %s
        """
        
        cursor.execute(update_query, (datetime.now(), row_id))
        conn.commit()
        cursor.close()
        conn.close()
        return True
    except Exception as e:
        logger.error(f"Error marking invalid IP for row {row_id}: {e}")
        return False

def process_batch():
    """
    处理一批IP地址
    """
    ips = get_unprocessed_ips()
    
    if not ips:
        logger.info("No unprocessed IPs found.")
        return 0
    
    logger.info(f"Processing {len(ips)} IP addresses")
    processed_count = 0
    
    for row_id, ip in ips:
        try:
            geo_data = get_ip_geolocation(ip)
            if geo_data:
                if update_ip_geolocation(row_id, geo_data):
                    logger.info(f"Updated geolocation for IP {ip} (Row ID: {row_id})")
                    processed_count += 1
            else:
                # 即使无法获取地理位置，也标记为已处理，避免反复尝试
                mark_invalid_ip(row_id)
                logger.warning(f"Marked IP {ip} as processed (Row ID: {row_id})")
            
            # 遵守API请求频率限制
            time.sleep(QUERY_INTERVAL)
        except Exception as e:
            logger.error(f"Error processing IP {ip} (Row ID: {row_id}): {e}")
    
    return processed_count

def main():
    """
    主函数
    """
    logger.info("Starting IP Geolocation Service")
    
    # 确保地理位置列存在
    if not ensure_geo_columns_exist():
        logger.error("Failed to ensure geo columns exist. Exiting.")
        return
    
    # 主循环
    while True:
        try:
            processed_count = process_batch()
            logger.info(f"Processed {processed_count} IP addresses")
            
            # 如果没有处理任何记录，可能是所有IP都已处理，等待更长时间
            if processed_count == 0:
                logger.info(f"No IP addresses processed. Waiting for {SLEEP_TIME * 5} seconds.")
                time.sleep(SLEEP_TIME * 5)
            else:
                logger.info(f"Waiting for {SLEEP_TIME} seconds before next batch.")
                time.sleep(SLEEP_TIME)
        except Exception as e:
            logger.error(f"Error in main loop: {e}")
            logger.info(f"Retrying in {SLEEP_TIME} seconds.")
            time.sleep(SLEEP_TIME)

if __name__ == "__main__":
    main() 