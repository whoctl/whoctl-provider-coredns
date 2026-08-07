$ORIGIN example.com.
$TTL 3600
@   IN  SOA ns1.example.com. hostmaster.example.com. (
        2026080501 ; serial
        7200       ; refresh
        3600       ; retry
        1209600    ; expire
        3600 )     ; minimum

    IN  NS      ns1.example.com.
    IN  NS      ns2.example.com.
    IN  MX  10  mail.example.com.

ns1     IN  A       192.0.2.53
ns2     IN  A       192.0.2.54
mail    IN  A       192.0.2.25
www     IN  CNAME   example.com.
api     600 IN  A   192.0.2.80
_dmarc  IN  TXT     "v=DMARC1; p=none; rua=mailto:dmarc@example.com"
