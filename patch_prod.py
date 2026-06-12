import yaml

# Read docker-compose.prod.yml
with open("docker-compose.prod.yml", "r") as f:
    content = f.read()

# Replace Redis Service Block
old_redis = """  # ----------------------------------------------------
  # 2. CACHE & ATOMIC COUNTER (REDIS)
  # ----------------------------------------------------
  redis:
    image: docker.io/redis:${REDIS_VERSION:-7-alpine}
    container_name: flashsale-redis
    command: redis-server --maxmemory 512mb --maxmemory-policy allkeys-lru
    ports:
      # Di laptop, ini akan dibind ke port non-standar lokal (misal: 16379)
      - "${REDIS_HOST_BIND:-127.0.0.1}:${REDIS_PORT:-16379}:${REDIS_INTERNAL_PORT:-6379}"
    networks:
      - flashsale_net
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5"""

new_redis = """  # ----------------------------------------------------
  # 2. CACHE & ATOMIC COUNTER (REDIS SENTINEL HA)
  # ----------------------------------------------------
  redis-master:
    image: docker.io/redis:${REDIS_VERSION:-7-alpine}
    container_name: flashsale-redis-master
    command: redis-server --appendonly yes --maxmemory 512mb --maxmemory-policy allkeys-lru
    ports:
      - "${REDIS_HOST_BIND:-127.0.0.1}:${REDIS_PORT:-16379}:${REDIS_INTERNAL_PORT:-6379}"
    networks:
      - flashsale_net
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5

  redis-replica-1:
    image: docker.io/redis:${REDIS_VERSION:-7-alpine}
    container_name: flashsale-redis-replica-1
    command: redis-server --replicaof redis-master 6379 --appendonly yes --maxmemory 512mb --maxmemory-policy allkeys-lru
    depends_on:
      redis-master:
        condition: service_healthy
    networks:
      - flashsale_net

  redis-replica-2:
    image: docker.io/redis:${REDIS_VERSION:-7-alpine}
    container_name: flashsale-redis-replica-2
    command: redis-server --replicaof redis-master 6379 --appendonly yes --maxmemory 512mb --maxmemory-policy allkeys-lru
    depends_on:
      redis-master:
        condition: service_healthy
    networks:
      - flashsale_net

  redis-sentinel-1:
    image: docker.io/bitnami/redis-sentinel:7.0
    container_name: flashsale-redis-sentinel-1
    environment:
      - REDIS_MASTER_HOST=redis-master
      - REDIS_MASTER_PORT_NUMBER=6379
      - REDIS_MASTER_SET=mymaster
      - REDIS_SENTINEL_QUORUM=2
      - REDIS_SENTINEL_DOWN_AFTER_MILLISECONDS=3000
    depends_on:
      - redis-master
      - redis-replica-1
      - redis-replica-2
    ports:
      - "26379:26379"
    networks:
      - flashsale_net

  redis-sentinel-2:
    image: docker.io/bitnami/redis-sentinel:7.0
    container_name: flashsale-redis-sentinel-2
    environment:
      - REDIS_MASTER_HOST=redis-master
      - REDIS_MASTER_PORT_NUMBER=6379
      - REDIS_MASTER_SET=mymaster
      - REDIS_SENTINEL_QUORUM=2
      - REDIS_SENTINEL_DOWN_AFTER_MILLISECONDS=3000
    depends_on:
      - redis-master
      - redis-replica-1
      - redis-replica-2
    networks:
      - flashsale_net

  redis-sentinel-3:
    image: docker.io/bitnami/redis-sentinel:7.0
    container_name: flashsale-redis-sentinel-3
    environment:
      - REDIS_MASTER_HOST=redis-master
      - REDIS_MASTER_PORT_NUMBER=6379
      - REDIS_MASTER_SET=mymaster
      - REDIS_SENTINEL_QUORUM=2
      - REDIS_SENTINEL_DOWN_AFTER_MILLISECONDS=3000
    depends_on:
      - redis-master
      - redis-replica-1
      - redis-replica-2
    networks:
      - flashsale_net"""

content = content.replace(old_redis, new_redis)

# Replace depends_on redis
content = content.replace("""      redis:
        condition: service_healthy""", """      redis-master:
        condition: service_healthy
      redis-sentinel-1:
        condition: service_started""")

# Replace env vars in go services
old_env = """      - REDIS_HOST=${REDIS_INTERNAL_HOST:-redis}
      - REDIS_PORT=${REDIS_INTERNAL_PORT:-6379}
      - REDIS_ADDR=${REDIS_INTERNAL_HOST:-redis}:${REDIS_INTERNAL_PORT:-6379}"""

new_env = """      - REDIS_SENTINEL_ADDRS=redis-sentinel-1:26379,redis-sentinel-2:26379,redis-sentinel-3:26379
      - REDIS_MASTER_NAME=mymaster"""

content = content.replace(old_env, new_env)

with open("docker-compose.prod.yml", "w") as f:
    f.write(content)
