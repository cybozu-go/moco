CREATE DATABASE IF NOT EXISTS test;
CREATE TABLE IF NOT EXISTS `test`.`t1` (
`num` bigint unsigned NOT NULL AUTO_INCREMENT,
`val0` varchar(100) DEFAULT NULL,
`val1` varchar(100) DEFAULT NULL,
`val2` varchar(100) DEFAULT NULL,
`val3` varchar(100) DEFAULT NULL,
`val4` varchar(100) DEFAULT NULL,
  UNIQUE KEY `num` (`num`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
