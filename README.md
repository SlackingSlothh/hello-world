# DataDock

## Menjalankan aplikasi

Siapkan Docker dan Docker Compose. Jalankan dari folder utama proyek:

```sh
docker compose up -d --build
```

Buka http://localhost, lalu login dengan akun admin default:

- Username: `admin`
- Password: `admin-local-change-me`

Untuk mencoba secara lokal, tidak perlu membuat `.env`. Jika ingin mengubah
konfigurasi, salin `.env.example` menjadi `.env` sebelum menjalankan aplikasi.

## Menambahkan database contoh

Database contoh bioskop sudah disiapkan oleh Compose beserta datanya.
Untuk mengaksesnya, buka **Manage connections → New connection** dan isi:

- Connection name: `Bioskop`
- Database engine: `PostgreSQL`
- Maximum open connections: `5`
- Maximum idle connections: `2`

Isi DSN dengan:

```text
postgres://example_user:example_local_password@db_example:5432/example_db?sslmode=disable
```

DSN adalah string berisi alamat database, nama database, username, dan password.
Contoh di atas berlaku untuk konfigurasi Compose default.

Simpan, lalu buka **Databases** dan klik kartu Bioskop untuk melihat tabel dan data.
Koneksi yang sudah disimpan tetap tersedia setelah aplikasi dijalankan ulang.

## Setelah mengubah kode

Bangun ulang backend dan frontend:

```sh
docker compose up -d --build app frontend
```

## Menghentikan aplikasi

```sh
docker compose down
```

Perintah ini tidak menghapus data database yang tersimpan di volume Docker.
