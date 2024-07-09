resource "aws_db_subnet_group" "rds" {
  count       = var.data_layer_remote ? 1 : 0
  name        = local.e3s_rds_subnet_name
  description = "RDS subnet group"
  subnet_ids  = [for subnet in aws_subnet.private_per_zone : subnet.id]
}

resource "aws_db_instance" "postgres" {
  count                      = var.data_layer_remote ? 1 : 0
  db_name                    = local.e3s_rds_db_name
  allocated_storage          = 10
  max_allocated_storage      = 30
  instance_class             = "db.t4g.small"
  engine                     = "postgres"
  engine_version             = "13.13"
  username                   = var.remote_db.username
  password                   = var.remote_db.pass
  auto_minor_version_upgrade = true
  db_subnet_group_name       = aws_db_subnet_group.rds[0].name
  port                       = 5432

  maintenance_window      = "Mon:00:00-Mon:01:00"
  backup_window           = "01:01-02:00"
  backup_retention_period = 2
  storage_encrypted       = true

  deletion_protection      = false
  apply_immediately        = false
  skip_final_snapshot      = true
  delete_automated_backups = true

  vpc_security_group_ids = [aws_security_group.rds[0].id]
}


# resource "aws_rds_cluster" "aurora" {
#   engine                       = "aurora-postgresql"
#   storage_type                 = "aurora-iopt1"
#   port                         = 5432
#   db_subnet_group_name         = aws_db_subnet_group.rds.name
#   master_username              = var.remote_db.username
#   master_password              = var.remote_db.pass
#   preferred_maintenance_window = "Mon:00:00-Mon:01:00"
#   preferred_backup_window      = "01:01-02:00"
#   backup_retention_period      = 2
#   storage_encrypted            = true

#   deletion_protection      = false
#   apply_immediately        = true
#   skip_final_snapshot      = true
#   delete_automated_backups = true

#   vpc_security_group_ids = [aws_security_group.rds.id]
# }

# resource "aws_rds_cluster_instance" "aurora_instance" {
#   cluster_identifier = aws_rds_cluster.aurora.id
#   // aurora doesn't support db.t4g.small
#   instance_class             = "db.t4g.medium"
#   engine                     = aws_rds_cluster.aurora.engine
#   engine_version             = aws_rds_cluster.aurora.engine_version
#   db_subnet_group_name       = aws_db_subnet_group.rds.name
#   auto_minor_version_upgrade = true
# }
