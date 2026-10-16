# Docker FS Viewer

macOS CLI для просмотра и редактирования root filesystem остановленного Linux-контейнера Docker Desktop через Finder.

Утилита экспортирует rootfs в case-sensitive APFS sparse bundle, монтирует его как обычный том и открывает в Finder. Команда `apply` создаёт изменённый image, переименовывает исходный контейнер в backup и создаёт остановленный replacement под исходным именем.

## Требования

- macOS 12+ с APFS и `hdiutil`
- Docker Desktop с запущенным Engine
- Go 1.22+ и Xcode Command Line Tools для сборки
- только остановленные Linux-контейнеры

Сторонние filesystem extensions, macFUSE и root-доступ не нужны.

## Сборка

```sh
make build
./bin/docker-fs-viewer help
```

На macOS 26 установленный Go 1.22 может создавать binary без `LC_UUID` внутренним linker. Поэтому Makefile использует системный linker через `-linkmode=external`.

## Использование

```sh
# Найти остановленный контейнер
./bin/docker-fs-viewer list

# Создать сессию, смонтировать APFS-том и открыть Finder
./bin/docker-fs-viewer mount my-container

# Показать добавленные, изменённые и удалённые файлы
./bin/docker-fs-viewer status [SESSION]

# Preview, подтверждение и транзакционное применение
./bin/docker-fs-viewer apply [--yes] [SESSION]

# Откатить Apply: удалить остановленный replacement и вернуть backup
./bin/docker-fs-viewer rollback [--yes] [SESSION]

# Отсоединить том, сохранив рабочую копию
./bin/docker-fs-viewer unmount [--force] [SESSION]

# Показать сохранённые сессии
./bin/docker-fs-viewer sessions
```

Если `SESSION` не указан, используется последняя созданная сессия. Данные сессий находятся в `~/Library/Application Support/docker-fs-viewer/sessions`.

## Что происходит при Apply

1. Утилита убеждается, что исходный контейнер остановлен.
2. Повторно экспортирует rootfs и сравнивает digest со снимком. Внешние изменения блокируют Apply.
3. Собирает полный tar с изменениями и исходными Linux-метаданными.
4. Импортирует временный image.
5. Переименовывает исходный контейнер в `<name>.docker-fs-backup-<timestamp>`.
6. Создаёт replacement под исходным именем, сохраняя container config, host config, labels, restart policy, mounts и network attachments.

Replacement остаётся остановленным. Его container ID отличается от исходного. Backup не удаляется автоматически.

При ошибке создания replacement исходному контейнеру возвращается его имя, а временный image удаляется.

## Volumes и bind mounts

Docker export не включает содержимое mounted volumes и bind mounts. Finder показывает underlying rootfs в соответствующей точке, а не данные внешнего mount.

Apply не меняет внешние данные. Replacement получает те же volume/bind mount настройки, поэтому при запуске Docker снова скроет underlying directory содержимым внешнего mount.

## Метаданные и ограничения

- UID/GID, mode, symlink, hardlink, device/FIFO headers, PAX records и xattrs исходных entries хранятся в sidecar manifest.
- Обычные файлы и symlink доступны в Finder. Device nodes и FIFO в Finder не материализуются, но без изменений переносятся в replacement.
- Новые entries наследуют UID/GID ближайшего исходного parent directory.
- Изменение POSIX mode существующего файла через Finder не переносится; исходный Linux mode сохраняется.
- Finder metadata (`.DS_Store`, `._*`, `.Spotlight-V100`, `.Trashes`, `.fseventsd`) игнорируются.
- Container image history при импорте flatten-ится. Runtime configuration контейнера сохраняется отдельно.
- Namespace modes вида `container:<id>` для network/IPC/PID/UTS отклоняются как небезопасные для автоматического пересоздания.
- Запуск replacement до rollback блокирует rollback: сначала остановите его.

## Безопасность

Container rootfs считается недоверенным вводом. Распаковка отклоняет absolute paths, `..` и попытки пройти через symlink parent. Apply всегда показывает diff и требует подтверждение, если не передан `--yes`.

Рабочий APFS image может содержать секреты контейнера в открытом виде. После завершения отсоедините том и удалите ненужную директорию сессии вручную.

## Тесты

```sh
make test

# Требует Docker Desktop и локальный nginx:1.27-alpine,
# либо задайте другой локальный image.
DOCKER_FS_VIEWER_TEST_IMAGE=nginx:1.27-alpine make integration
```

Integration test создаёт отдельный контейнер и volume, проверяет mount, add/apply, повторное подключение volume и rollback, затем удаляет fixture.

Подробности устройства: [docs/architecture.md](docs/architecture.md).
