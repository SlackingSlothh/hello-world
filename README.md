# DataDock

DataDock mengelola koneksi database, akses berdasarkan grup, dan riwayat migrasi.

## Menjalankan aplikasi

Jalankan dari root repo dengan Docker Compose.

```sh
docker compose up -d --build
```

Buka <http://localhost> dan masuk dengan akun lokal `admin` / `admin-local-change-me`.
Untuk mengubah konfigurasi lokal, salin [`.env.example`](.env.example) ke `.env`.
Jika port frontend berubah, sesuaikan `PUBLIC_ORIGIN` dan `FRONTEND_ORIGINS`.
Gunakan kredensial dan kunci enkripsi sendiri untuk deployment.

## Database contoh

Buka **Connections → Add connection**, pilih PostgreSQL, dan masukkan DSN default berikut.

```text
postgres://example_user:example_local_password@db_example:5432/example_db?sslmode=disable
```

Compose mengimpor [dump bioskop](db_example/data.sql) saat volume contoh pertama
kali diinisialisasi. Halaman **Databases** menampilkan data mock. Pengelolaan
koneksi dan riwayat migrasi memakai API.

## Pengembangan

- Bangun ulang aplikasi dengan `docker compose up -d --build app frontend`.
- Hentikan layanan dan simpan volume dengan `docker compose down`.
- Lihat panduan [backend](backend/README.md) dan [frontend](frontend/README.md).
