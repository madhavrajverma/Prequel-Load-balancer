#### The Comparison Experiment

### running prequal 
```bash
# terminal 1-3: servers  running
# terminal 4: prequal
go run cmd/lb/main.go \
  --backends=http://localhost:9001,http://localhost:9002,http://localhost:9003 \
  --algo=prequal

# terminal 5: generator
go run cmd/gen/main.go --rps=100 --dur=120s --c=50

# terminal 6: after 30 seconds inject slow server
sleep 30 && curl "http://localhost:9003/control?mode=slow"

# expected: p99 in generator output stays low with prequal
```

```bash
- Result

[    5s] requests=432 errors=0 rps=86 mean=87ms p50=86ms p99=130ms p999=249ms
[   10s] requests=853 errors=0 rps=85 mean=87ms p50=86ms p99=131ms p999=249ms
[   15s] requests=1282 errors=0 rps=85 mean=86ms p50=85ms p99=209ms p999=249ms
[   20s] requests=1691 errors=0 rps=85 mean=87ms p50=85ms p99=216ms p999=265ms
[   25s] requests=2126 errors=0 rps=85 mean=87ms p50=85ms p99=214ms p999=269ms
[   30s] requests=2577 errors=0 rps=86 mean=87ms p50=85ms p99=220ms p999=276ms
[   35s] requests=3014 errors=0 rps=86 mean=88ms p50=84ms p99=269ms p999=323ms
[   40s] requests=3429 errors=0 rps=86 mean=86ms p50=83ms p99=250ms p999=323ms
[   45s] requests=3875 errors=0 rps=86 mean=85ms p50=82ms p99=248ms p999=323ms
[   50s] requests=4283 errors=0 rps=86 mean=84ms p50=81ms p99=243ms p999=319ms
[   55s] requests=4736 errors=0 rps=86 mean=84ms p50=81ms p99=240ms p999=323ms
[   60s] requests=5184 errors=0 rps=86 mean=83ms p50=80ms p99=238ms p999=323ms
[   65s] requests=5616 errors=0 rps=86 mean=83ms p50=80ms p99=235ms p999=323ms
[   70s] requests=6048 errors=0 rps=86 mean=82ms p50=80ms p99=233ms p999=319ms
[   75s] requests=6487 errors=0 rps=86 mean=82ms p50=79ms p99=232ms p999=319ms
[   80s] requests=6923 errors=0 rps=87 mean=81ms p50=79ms p99=229ms p999=319ms
[   85s] requests=7364 errors=0 rps=87 mean=81ms p50=79ms p99=229ms p999=319ms
[   90s] requests=7780 errors=0 rps=86 mean=81ms p50=78ms p99=229ms p999=319ms
[   95s] requests=8187 errors=0 rps=86 mean=81ms p50=78ms p99=229ms p999=318ms
[  100s] requests=8597 errors=0 rps=86 mean=81ms p50=78ms p99=229ms p999=318ms
[  105s] requests=9033 errors=0 rps=86 mean=81ms p50=78ms p99=229ms p999=316ms
[  110s] requests=9479 errors=0 rps=86 mean=80ms p50=78ms p99=226ms p999=316ms
[  115s] requests=9921 errors=0 rps=86 mean=80ms p50=77ms p99=225ms p999=316ms
[  120s] requests=10348 errors=0 rps=86 mean=80ms p50=77ms p99=225ms p999=314ms

 final results
[  120s] requests=10352 errors=0 rps=86 mean=80ms p50=77ms p99=225ms p999=314ms
total duration: 2m0.078s
```

### running wrr

```bash
# restore server3
curl "http://localhost:9003/control?mode=normal"

# restart with wrr
go run cmd/lb/main.go \
  --backends=http://localhost:9001,http://localhost:9002,http://localhost:9003 \
  --algo=wrr

# run generator again
go run cmd/gen/main.go --rps=100 --dur=120s --c=50

# inject slow server at same point
sleep 30 && curl "http://localhost:9003/control?mode=slow"

# expected : p99 spikes and recovers slowly with wrr
```

```bash
- Result

[    5s] requests=445 errors=0 rps=89 mean=82ms p50=80ms p99=221ms p999=280ms
[   10s] requests=879 errors=0 rps=88 mean=81ms p50=80ms p99=130ms p999=280ms
[   15s] requests=1303 errors=0 rps=87 mean=82ms p50=81ms p99=130ms p999=256ms
[   20s] requests=1720 errors=0 rps=86 mean=82ms p50=81ms p99=130ms p999=256ms
[   25s] requests=2168 errors=0 rps=87 mean=83ms p50=82ms p99=130ms p999=248ms
[   30s] requests=2579 errors=0 rps=86 mean=83ms p50=82ms p99=204ms p999=256ms
[   35s] requests=3021 errors=0 rps=86 mean=88ms p50=82ms p99=313ms p999=328ms
[   40s] requests=3480 errors=0 rps=87 mean=91ms p50=82ms p99=319ms p999=330ms
[   45s] requests=3900 errors=0 rps=87 mean=92ms p50=82ms p99=319ms p999=330ms
[   50s] requests=4330 errors=0 rps=87 mean=93ms p50=81ms p99=321ms p999=330ms
[   55s] requests=4764 errors=0 rps=87 mean=94ms p50=81ms p99=321ms p999=330ms
[   60s] requests=5207 errors=0 rps=87 mean=95ms p50=81ms p99=322ms p999=330ms
[   65s] requests=5654 errors=0 rps=87 mean=96ms p50=81ms p99=323ms p999=331ms
[   70s] requests=6047 errors=0 rps=86 mean=96ms p50=81ms p99=323ms p999=330ms
[   75s] requests=6491 errors=0 rps=87 mean=97ms p50=80ms p99=324ms p999=331ms
[   80s] requests=6917 errors=0 rps=86 mean=97ms p50=80ms p99=324ms p999=331ms
[   85s] requests=7345 errors=0 rps=86 mean=97ms p50=80ms p99=324ms p999=331ms
[   90s] requests=7772 errors=0 rps=86 mean=97ms p50=80ms p99=324ms p999=331ms
[   95s] requests=8212 errors=0 rps=86 mean=98ms p50=80ms p99=324ms p999=433ms
[  100s] requests=8651 errors=0 rps=87 mean=98ms p50=80ms p99=325ms p999=433ms
[  105s] requests=9100 errors=0 rps=87 mean=98ms p50=80ms p99=325ms p999=432ms
[  110s] requests=9512 errors=0 rps=86 mean=98ms p50=80ms p99=325ms p999=433ms
[  115s] requests=9967 errors=0 rps=87 mean=98ms p50=80ms p99=325ms p999=433ms
[  120s] requests=10385 errors=0 rps=87 mean=98ms p50=80ms p99=325ms p999=433ms

 final results
[  120s] requests=10392 errors=0 rps=86 mean=98ms p50=80ms p99=325ms p999=433ms
total duration: 2m0.256s

```
