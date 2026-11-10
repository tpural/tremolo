# A cert-manager certificate that expires soon or can't be issued: renewals
# normally happen 30 days ahead, so 14 days left means renewal is failing.
EVERY = "15m"
FOR = "1h"

def check(now):
    out = []
    left = "min by (exported_namespace, name) (certmanager_certificate_expiration_timestamp_seconds) - time()"
    for s in metrics("(%s) < 14 * 86400" % left):
        days = int(s.value / 86400)
        out.append(alert(
            "certificate %s expires in %d days" % (cert(s), days),
            key = cert(s),
            severity = "page" if days < 3 else "warn",
            namespace = s.labels["exported_namespace"],
        ))
    for s in metrics('max by (exported_namespace, name) (certmanager_certificate_ready_status{condition="False"}) == 1'):
        out.append(alert("certificate %s is not ready" % cert(s), key = cert(s) + ":ready", namespace = s.labels["exported_namespace"]))
    return out

def cert(s):
    return s.labels["exported_namespace"] + "/" + s.labels["name"]
