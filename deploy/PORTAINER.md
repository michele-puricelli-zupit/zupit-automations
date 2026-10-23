# Produzione su Portainer

Il deploy segue l'infrastruttura di Zupit Eventi: immagine su GHCR, runner `zupit-agents` nel gruppo `Container`, server `zupit-applications`, Docker Standalone e Traefik sulla rete host. La pipeline gestisce lo stack; Portainer permette di ispezionarlo. Non usare questi file con Docker Swarm.

Lo stack `zupit-automations-production` ha rete e volume PostgreSQL propri e una sola istanza applicativa. Traefik scopre l'app sulla rete bridge e inoltra HTTPS alla porta interna 8080. App e database non pubblicano porte sull'host. Non aggiungere l'app alla rete speciale `host`.

## Prima del deploy

1. Abilitare per questo repository i workflow condivisi `zupit-it/pipeline-templates@v1.31.2`, i runner di build/deploy e la pubblicazione dei package GHCR. Il server deve supportare Docker Compose con i comandi `--wait` e i secrets da variabili d'ambiente, come l'installazione Eventi. Il runner di deploy deve controllare il Docker Engine persistente del server, non un daemon temporaneo.
2. Creare l'ambiente GitHub **production**. Riportare come Environment secrets i valori di [portainer.env.example](portainer.env.example), seguendo la convenzione della pipeline Eventi. Non aggiungere `APP_IMAGE_TAG`: viene assegnato dalla pipeline con il tag SHA del commit. Per un package privato, verificare che il token del repository possa pubblicarlo e scaricarlo.
3. Creare il DNS di `automazioni.zupit.software` verso il server Traefik. Il nome è proposto: se cambia, aggiornare `APP_HOSTNAME` e l'URL dell'ambiente nel workflow. Verificare gli entrypoint `web`, `websecure` e il resolver `zupit-software-resolver`, oppure configurare `TRAEFIK_CERT_RESOLVER`.
4. Creare un client Google OAuth **Web**, dedicato a questa applicazione, con consenso interno Workspace e queste callback esatte:

   ```text
   https://automazioni.zupit.software/auth/callback
   https://automazioni.zupit.software/auth/gmail/callback
   ```

   Abilitare Gmail API e Pub/Sub API e autorizzare `openid`, `email`, `profile`, `gmail.readonly`, `gmail.send`. Il topic Gmail deve appartenere al progetto Google del client OAuth. Dare a `gmail-api-push@system.gserviceaccount.com` il ruolo Publisher sul topic. L'identità ADC in `GOOGLE_CREDENTIALS_JSON` deve poter consumare la subscription pull, con ruolo Pub/Sub Subscriber. Il runtime deve raggiungere Google, Jira, Slack e, se usato, TypeSafe via HTTPS; Pub/Sub non richiede un webhook pubblico.
5. Preparare un database nuovo, oppure pianificare esplicitamente il trasferimento di quello esistente. Per un database nuovo generare password PostgreSQL e CSRF con `openssl rand -hex 32`, e `ENCRYPTION_KEY` con `openssl rand -base64 32`. La password nella `DATABASE_URL` deve corrispondere a `POSTGRES_PASSWORD` ed essere percent-encoded. Il ruolo e il database sono entrambi `automations`.

Gli altri segreti sono montati come file sotto `/run/secrets`. `GOOGLE_CREDENTIALS_JSON` contiene il JSON ADC completo, non il percorso locale. `TYPESAFE_API_KEY` è facoltativo ed è passato all'environment dell'app; ometterlo lascia disponibile solo A. Per il confronto B servono anche la connessione Slack, il DM e gli scope descritti nel README. Jira e Slack vengono collegati dall'interfaccia, con token cifrati nel database.

I file `deploy/*.env` e `deploy/backups/` sono esclusi da Git e dal contesto Docker. Non inserire segreti nei file di esempio. Se si importa un database, conservare la sua `ENCRYPTION_KEY`: cambiarla rende illeggibili i token già cifrati. Conservare la chiave anche nei backup operativi. Cambiare `POSTGRES_PASSWORD` dopo l'inizializzazione del volume non modifica automaticamente la password del ruolo PostgreSQL.

## Primo avvio

1. Pubblicare il codice sul repository. Avviare **Deploy production** da GitHub Actions selezionando il commit/branch da rilasciare; anche un tag `v*` avvia il deploy. I push senza tag eseguono solo la verifica. Per richiedere approvazione umana a ogni rilascio, configurare i required reviewers dell'ambiente `production`.
2. Attendere `verify`, build e push su GHCR. La build usa il Dockerfile della root e un tag uguale allo SHA del commit, non `latest`. La verifica è riutilizzata da `verify.yml`, con PostgreSQL di test separato.
3. Il deploy valida Compose, scarica le immagini, ferma l'app esistente, attende PostgreSQL, applica le migrazioni con il nuovo binario e riavvia l'app solo se le migrazioni terminano con codice 0. Il job attende la readiness. Il container `migrate` terminato con codice 0 è normale.
4. Verificare:

   ```sh
   curl -fsS https://automazioni.zupit.software/health/live
   curl -fsS https://automazioni.zupit.software/health/ready
   ```

   Entrambi restituiscono `ok`. Accedere con Google usando un indirizzo in `ADMIN_EMAILS`: non serve il comando `userctl` di Eventi.
5. Collegare le caselle Gmail e le destinazioni Jira/Slack. Configurare i flussi inizialmente disattivati, salvarli e simularli. Pianificare il passaggio: fermare l'elaborazione locale e gli scenari Make equivalenti prima di attivare la stessa automazione in produzione. Fermare anche l'app locale quando si trasferisce la stessa casella: il suo watch Gmail e il consumer Pub/Sub possono interferire con la configurazione di produzione. Annotare l'orario e riconciliare manualmente le email dell'intervallo; le email precedenti all'attivazione non vengono rielaborate automaticamente.
6. Attivare un flusso per volta e inviare una nuova email di prova. Controllare **Esecuzioni**, il riferimento esterno, i campi creati e la presenza nella board/coda attesa.

Non avviare in parallelo deploy dalla pipeline e aggiornamenti Portainer. Il workflow serializza i rilasci in produzione e non interrompe automaticamente quello in corso.

## Aggiornamenti, backup e rollback

Prima di ogni aggiornamento annotare immagine/digest attuale ed eseguire un backup PostgreSQL su storage separato. Il workflow non esegue backup automatici. Un esempio dalla directory del checkout sul server, con la configurazione di produzione già disponibile nell'ambiente:

```sh
export COMPOSE_PROJECT_NAME=zupit-automations-production
export COMPOSE_FILE=deploy/compose.portainer.yaml
umask 077
mkdir -p /srv/backups/zupit-automations
docker compose exec -T postgres pg_dump -U automations -d automations -Fc \
  > /srv/backups/zupit-automations/automations-YYYYMMDD-HHMM.dump
```

Sostituire il nome del file a ogni backup. Verificare l'exit code e il dump con `pg_restore --list`, copiarlo su storage off-host cifrato e provare periodicamente il ripristino in un database isolato. Conservare separatamente `ENCRYPTION_KEY` e configurazione necessaria al recupero. Monitorare spazio disponibile e backup: un disco Docker pieno blocca PostgreSQL. La retention dello storico applicativo non è automatica.

Il deploy interrompe brevemente l'app. Se migrazione o readiness falliscono, il job fallisce e non dichiara il rilascio riuscito: leggere i log di `migrate` e `app` in Portainer. L'app resta ferma se la migrazione fallisce. Non avviare il vecchio binario su uno schema incompatibile.

Per ripetere manualmente la sequenza, dopo aver configurato tutte le variabili e `APP_IMAGE_TAG` all'immagine pubblicata:

```sh
docker compose config --quiet
docker compose pull
docker compose stop app
docker compose up -d --wait --wait-timeout 120 postgres
docker compose up --no-deps --force-recreate --exit-code-from migrate migrate
docker compose up -d --no-deps --wait --wait-timeout 120 app
```

Eseguire un comando alla volta e fermarsi al primo errore. Per il rollback usare l'immagine/digest precedente solo con uno schema compatibile; altrimenti occorre ripristinare anche il backup del database. Non eliminare il volume e non usare `down -v`.

## Verifica locale dei file

`make test` esegue anche i test del Compose di produzione se Docker Compose è installato: verificano separazione delle porte, immagine condivisa tra app/migrazione, dipendenze di avvio e configurazione obbligatoria. `make verify` aggiunge race detector, generazione, vet e build. I test non avviano lo stack di produzione.
