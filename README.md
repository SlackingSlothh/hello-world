# Menjalankan Aplikasi

Prasyarat: Docker dengan Docker Compose.

Jalankan dari root proyek:

```sh
docker compose up --build -d
```

Buka http://localhost:8080.

PostgreSQL tersedia di `localhost:5433`, database dan username `hello_world`,
password `local_dev_password`. Backend belum terhubung ke database.

Lihat log:

```sh
docker compose logs -f app
```

Hentikan aplikasi:

```sh
docker compose down
```
