# Betikler

Conclave'i çalıştırmak ve durdurmak için. Masaüstünde `Conclave` ve
`Conclave - Kapat` kısayolları bu iki betiği çağırır.

| Betik | Ne yapar |
|---|---|
| `conclave-baslat.cmd` | Daemon çalışmıyorsa başlatır, sonra arayüzü açar. |
| `conclave-durdur.cmd` | Önce arayüzü, sonra daemon'u kapatır. |
| `conclave-gelistir.cmd` | `wails dev` ile canlı yeniden yükleme; frontend değişikliği için build gerekmez. |

`conclave-baslat.cmd` ve `conclave-durdur.cmd` hem geliştirme ağacında hem kurulu
bir Conclave'de çalışır: başlatma betiği önce kendi yanındaki `conclave.exe` ve
`conclave-desktop.exe`'ye bakar, bulamazsa repo düzenini dener.

Kurulum ve güncelleme betiği ayrı yerde: repo kökündeki `install.ps1`. Yayınlanan
zip'e ve installer'a o dosya giriyor, uygulamadaki **Güncelle** düğmesi de onu
çağırıyor.

## Pencereyi kapatmak daemon'u durdurmaz

Bu bilerek böyle: arayüz kapalıyken de kartlar çalışmaya devam eder, sağlayıcılar
cevap üretmeyi sürdürür. Her şeyi kapatmak istediğinde `conclave-durdur.cmd`
kullan. Yarım kalan işler SQLite'ta durur ve bir sonraki başlatışta kuyruğa
alınır.

## Ne zaman derlemek gerekir

| Ne değişti | Gereken |
|---|---|
| `frontend/src/**` (tsx, css) | `cd cmd\conclave-desktop && wails build` — ya da `conclave-gelistir.cmd` ile hiç. |
| `cmd/conclave-desktop/*.go` | Aynı: `wails build`. |
| `internal/**`, `cmd/conclave/**` | `go build -o build\conclave.exe .\cmd\conclave`, sonra daemon'u yeniden başlat. |

Üretim derlemesinde frontend binary'nin içine gömülür, bu yüzden bir CSS
değişikliği bile `wails build` ister. `wails dev` bunu atlar: dosyayı
kaydettiğin anda pencerede görürsün.

`internal/` altındaki bir değişiklik hem daemon'u hem arayüzü ilgilendirir —
arayüz de aynı paketleri kullanır — ama davranış daemon'da olduğu için pratikte
daemon'u yeniden derleyip başlatmak yeter. Wails bindings'i değişen bir Go tipi
eklediysen (`app.go` içindeki metotlar, `domain` tipleri) `wails build` de gerekir.
