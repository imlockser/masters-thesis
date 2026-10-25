from collections import defaultdict
from datetime import date, datetime
from pathlib import Path
from zipfile import ZipFile
from zoneinfo import ZoneInfo

import pandas as pd
from lxml.etree import iterparse, parse
from open_mastr.utils.unzip_http import RemoteZipFile
from open_mastr.xml_download.utils_download_bulk import gen_xml_download_url


def prepare_installed_pv_capacity(
    start_date: str,
    end_date: str,
    output_dir: str,
    file_name: str,
    snapshot_date: str | None = None,
) -> pd.DataFrame:
    start = pd.Timestamp(start_date, tz="UTC")
    end = pd.Timestamp(end_date, tz="UTC") + pd.Timedelta(days=1)

    if start >= end:
        raise ValueError("start_date must not be after end_date")

    folder = Path(__file__).resolve().parents[4] / output_dir
    path = folder / file_name
    snapshot = (
        date.fromisoformat(snapshot_date)
        if snapshot_date is not None
        else datetime.now(ZoneInfo("Europe/Berlin")).date()
    )
    archive_path = folder / "mastr" / f"{snapshot.isoformat()}.zip"
    archive_path.parent.mkdir(parents=True, exist_ok=True)

    remote = RemoteZipFile(gen_xml_download_url(snapshot.timetuple()))
    names = [
        name
        for name in remote.files
        if name.lower().split("_")[0].split(".")[0]
        in {"einheitensolar", "katalogwerte"}
    ]

    temporary_archive = archive_path.with_suffix(".tmp")

    with ZipFile(temporary_archive, "w"):
        pass

    for name in names:
        remote.extractzip(name, path=temporary_archive)

    temporary_archive.replace(archive_path)

    daily: defaultdict[str, float] = defaultdict(float)

    with ZipFile(archive_path) as archive:
        with archive.open("Katalogwerte.xml") as stream:
            operating = {
                row.findtext("Id")
                for row in parse(stream).getroot()
                if row.findtext("Wert") == "In Betrieb"
            }

        for name in names:
            if not name.startswith("EinheitenSolar"):
                continue

            with archive.open(name) as stream:
                for _, row in iterparse(stream, events=("end",), tag="EinheitSolar"):
                    if row.findtext("EinheitBetriebsstatus") in operating:
                        commissioned = row.findtext("Inbetriebnahmedatum")
                        capacity = float(row.findtext("Nettonennleistung") or 0)
                        if commissioned and capacity > 0:
                            daily[commissioned[:10]] += capacity / 1000

                    row.clear()

                    while row.getprevious() is not None:
                        del row.getparent()[0]

    if not daily:
        raise ValueError("MaStR contains no usable operating PV units")

    installed = pd.DataFrame(sorted(daily.items()), columns=["date", "added_mw"])
    installed["date"] = pd.to_datetime(installed["date"]).dt.as_unit("ns")
    installed["installed_pv_capacity_mw"] = installed.added_mw.cumsum()

    hours = pd.date_range(start, end, freq="h", inclusive="left")
    dates = (
        pd.Series(hours)
        .dt.tz_convert("Europe/Berlin")
        .dt.tz_localize(None)
        .dt.normalize()
        .dt.as_unit("ns")
    )

    result = pd.DataFrame({"timestamp_utc": hours})
    result["installed_pv_capacity_mw"] = pd.merge_asof(
        pd.DataFrame({"date": dates}), installed, on="date"
    )["installed_pv_capacity_mw"].to_numpy()

    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(".tmp")
    result.to_parquet(temporary, index=False)
    temporary.replace(path)

    return result
